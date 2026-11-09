//! Files findings as taskwarrior tasks through a taskchampion replica that syncs with a server.
//!
//! taskchampion's `Server` is not `Send`, so its futures cannot live inside a tracker future.
//! One worker thread owns the replica and the server and runs every request on its own
//! current-thread runtime; callers talk to it over a channel.

use std::any::Any;
use std::fmt::Display;
use std::fs;
use std::future::Future;
use std::panic::AssertUnwindSafe;
use std::path::{Path, PathBuf};
use std::sync::{Arc, OnceLock};

use almanac::prelude::*;
use futures_util::FutureExt;
use taskchampion::chrono::Utc;
use taskchampion::storage::AccessMode;
use taskchampion::{
    Annotation, Operations, Replica, Server, ServerConfig, SqliteStorage, Status, Tag, TaskData,
    Uuid,
};
use tokio::sync::{mpsc, oneshot};

use crate::providers::body_text;
use crate::routing::Project;

/// The UDA holding the delivery key a task was filed under.
pub const KEY_ATTR: &str = "almanac_key";
/// The UDA holding the target the task was filed for.
pub const TARGET_ATTR: &str = "almanac_target";
/// The UDA holding the pull request a question is about.
pub const PULL_ATTR: &str = "almanac_pull";
/// The tag every filed task carries.
pub const TAG: &str = "almanac";
/// The tag a question carries.
pub const QUESTION_TAG: &str = "question";

pub struct TaskwarriorConfig {
    pub replica_dir: PathBuf,
    pub server: ServerConfig,
}

pub struct Taskwarrior {
    requests: mpsc::UnboundedSender<Request>,
    /// Why the worker stopped, set before its inbox closes so later requests can say.
    death: Arc<OnceLock<String>>,
}

/// One finding to file, already parsed and read on the caller side.
struct Delivery {
    key: String,
    target: String,
    project: String,
    title: String,
    body_text: String,
    question: bool,
    pull: Option<u64>,
}

enum Request {
    Deliver(Delivery, oneshot::Sender<Result<(), StepError>>),
    Sync(oneshot::Sender<Result<(), StepError>>),
    Tasks(oneshot::Sender<Result<Vec<TaskData>, StepError>>),
    #[cfg(test)]
    Crash(oneshot::Sender<Result<(), StepError>>),
}

/// Pulls the server's whole history into the replica at `config.replica_dir`, which must be new
/// or empty, and returns how many tasks arrived. A replica with no local changes has nothing to
/// push, so this reads without writing. Pulling forces every version through decryption, which
/// is what proves the client id and encryption secret match the history. An empty history proves
/// nothing, so it is an error.
pub async fn sync_check(config: TaskwarriorConfig) -> Result<usize, String> {
    let replica = Taskwarrior::new(config);
    replica.sync().await.map_err(|err| err.0)?;
    let pulled = replica.tasks().await.map_err(|err| err.0)?.len();
    if pulled == 0 {
        return Err("the server's history is empty, so decryption was not exercised".to_string());
    }
    Ok(pulled)
}

const WORKER_STOPPED: &str = "the taskwarrior worker stopped";

impl Taskwarrior {
    /// Spawns the worker thread eagerly. Nothing is opened until the first request.
    pub fn new(config: TaskwarriorConfig) -> Taskwarrior {
        let (requests, inbox) = mpsc::unbounded_channel();
        let death = Arc::new(OnceLock::new());
        let note = Arc::clone(&death);
        // The thread owns the inbox and drops it only after recording why it stopped, so a
        // request that finds the worker gone can always read the cause.
        let spawned = std::thread::Builder::new()
            .name("taskwarrior".to_string())
            .spawn(move || {
                let mut inbox = inbox;
                if let Err(cause) = run(config, &mut inbox) {
                    let _ = note.set(cause);
                }
            });
        if let Err(err) = spawned {
            let _ = death.set(format!("could not spawn the worker thread: {err}"));
        }
        Taskwarrior { requests, death }
    }

    /// Syncs the replica with the server.
    pub async fn sync(&self) -> Result<(), StepError> {
        self.request(Request::Sync).await
    }

    /// Every task in the replica, as of its last sync.
    pub async fn tasks(&self) -> Result<Vec<TaskData>, StepError> {
        self.request(Request::Tasks).await
    }

    async fn request<T>(
        &self,
        make: impl FnOnce(oneshot::Sender<Result<T, StepError>>) -> Request,
    ) -> Result<T, StepError> {
        let (reply, answer) = oneshot::channel();
        self.requests
            .send(make(reply))
            .map_err(|_| self.stopped())?;
        answer.await.map_err(|_| self.stopped())?
    }

    fn stopped(&self) -> StepError {
        StepError(match self.death.get() {
            Some(cause) => format!("{WORKER_STOPPED}: {cause}"),
            None => WORKER_STOPPED.to_string(),
        })
    }

    async fn deliver(
        &self,
        key: &str,
        target: &Slug,
        title: &str,
        body: &Untrusted<String>,
        question: bool,
        pull: Option<u64>,
    ) -> Result<(), StepError> {
        let project = match Project::from_target(target) {
            Some(parsed) => parsed.map_err(StepError)?,
            None => return Err(StepError(format!("{target} is not a task: target"))),
        };
        let delivery = Delivery {
            key: key.to_string(),
            target: target.to_string(),
            project: project.as_str().to_string(),
            title: title.to_string(),
            body_text: body_text(body)?,
            question,
            pull,
        };
        self.request(|reply| Request::Deliver(delivery, reply))
            .await
    }
}

impl Tracker for Taskwarrior {
    fn file<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        title: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        Box::pin(self.deliver(key, target, title, body, false, None))
    }

    fn ask<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        pull: Option<u64>,
        question: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        Box::pin(self.deliver(key, target, question, body, true, pull))
    }
}

/// The worker thread's body: serve requests until every sender is gone. An `Err` is why it
/// stopped early. A panic while serving is answered to its caller, then ends the worker, since
/// the half-used replica and server cannot be trusted or reopened.
fn run(
    config: TaskwarriorConfig,
    inbox: &mut mpsc::UnboundedReceiver<Request>,
) -> Result<(), String> {
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|err| format!("could not start the worker runtime: {err}"))?;
    runtime.block_on(async move {
        let mut worker = Worker {
            replica_dir: config.replica_dir,
            server_config: Some(config.server),
            replica: None,
            server: None,
        };
        while let Some(request) = inbox.recv().await {
            let panicked = match request {
                Request::Deliver(delivery, reply) => answer(worker.deliver(delivery), reply).await,
                Request::Sync(reply) => answer(worker.sync(), reply).await,
                Request::Tasks(reply) => answer(worker.tasks(), reply).await,
                #[cfg(test)]
                Request::Crash(reply) => answer(crash(), reply).await,
            };
            if let Some(cause) = panicked {
                return Err(cause);
            }
        }
        Ok(())
    })
}

/// Sends `work`'s result to `reply`. A panic becomes the caller's error and is returned as the
/// cause. A dropped reply means the caller went away; there is no one to tell.
async fn answer<T>(
    work: impl Future<Output = Result<T, StepError>>,
    reply: oneshot::Sender<Result<T, StepError>>,
) -> Option<String> {
    match AssertUnwindSafe(work).catch_unwind().await {
        Ok(result) => {
            let _ = reply.send(result);
            None
        }
        Err(payload) => {
            let cause = panic_message(payload);
            let _ = reply.send(Err(StepError(format!("{WORKER_STOPPED}: {cause}"))));
            Some(cause)
        }
    }
}

fn panic_message(payload: Box<dyn Any + Send>) -> String {
    match payload.downcast::<String>() {
        Ok(message) => *message,
        Err(payload) => match payload.downcast::<&str>() {
            Ok(message) => (*message).to_string(),
            Err(_) => "the worker panicked".to_string(),
        },
    }
}

#[cfg(test)]
async fn crash() -> Result<(), StepError> {
    panic!("boom")
}

/// Owns the replica and the server, each opened on first need. A failed open leaves the slot
/// empty, so the next request tries again.
struct Worker {
    replica_dir: PathBuf,
    server_config: Option<ServerConfig>,
    replica: Option<Replica<SqliteStorage>>,
    server: Option<Box<dyn Server>>,
}

impl Worker {
    async fn deliver(&mut self, delivery: Delivery) -> Result<(), StepError> {
        let replica = open_replica(&mut self.replica, &self.replica_dir).await?;
        let server = open_server(&mut self.server, &mut self.server_config).await?;
        sync(replica, server).await?;
        let known = replica
            .all_task_data()
            .await
            .map_err(|e| step(e, "could not read the taskwarrior replica"))?;
        if known
            .values()
            .any(|task| task.get(KEY_ATTR) == Some(delivery.key.as_str()))
        {
            return Ok(());
        }
        let ops = task_operations(replica, &delivery).await?;
        replica
            .commit_operations(ops)
            .await
            .map_err(|e| step(e, "could not commit the taskwarrior task"))?;
        sync(replica, server).await
    }

    async fn sync(&mut self) -> Result<(), StepError> {
        let replica = open_replica(&mut self.replica, &self.replica_dir).await?;
        let server = open_server(&mut self.server, &mut self.server_config).await?;
        sync(replica, server).await
    }

    async fn tasks(&mut self) -> Result<Vec<TaskData>, StepError> {
        let replica = open_replica(&mut self.replica, &self.replica_dir).await?;
        let all = replica
            .all_task_data()
            .await
            .map_err(|e| step(e, "could not read the taskwarrior replica"))?;
        Ok(all.into_values().collect())
    }
}

/// The operations that create the task for a delivery, not yet committed.
async fn task_operations(
    replica: &mut Replica<SqliteStorage>,
    delivery: &Delivery,
) -> Result<Operations, StepError> {
    let failed = |e| step(e, "could not build the taskwarrior task");
    let mut ops = Operations::new();
    let mut task = replica
        .create_task(Uuid::new_v4(), &mut ops)
        .await
        .map_err(failed)?;
    let now = Utc::now();
    task.set_description(delivery.title.clone(), &mut ops)
        .map_err(failed)?;
    task.set_status(Status::Pending, &mut ops).map_err(failed)?;
    task.set_entry(Some(now), &mut ops).map_err(failed)?;
    task.set_value("project", Some(delivery.project.clone()), &mut ops)
        .map_err(failed)?;
    let tag: Tag = TAG
        .parse()
        .map_err(|e| step(e, "could not build the taskwarrior task"))?;
    task.add_tag(&tag, &mut ops).map_err(failed)?;
    if delivery.question {
        let question: Tag = QUESTION_TAG
            .parse()
            .map_err(|e| step(e, "could not build the taskwarrior task"))?;
        task.add_tag(&question, &mut ops).map_err(failed)?;
    }
    task.add_annotation(
        Annotation {
            entry: now,
            description: format!("almanac {}\n{}", delivery.key, delivery.body_text),
        },
        &mut ops,
    )
    .map_err(failed)?;
    task.set_user_defined_attribute(KEY_ATTR, delivery.key.clone(), &mut ops)
        .map_err(failed)?;
    task.set_user_defined_attribute(TARGET_ATTR, delivery.target.clone(), &mut ops)
        .map_err(failed)?;
    if let Some(pull) = delivery.pull {
        task.set_user_defined_attribute(PULL_ATTR, pull.to_string(), &mut ops)
            .map_err(failed)?;
    }
    task.set_modified(now, &mut ops).map_err(failed)?;
    Ok(ops)
}

async fn open_replica<'a>(
    slot: &'a mut Option<Replica<SqliteStorage>>,
    dir: &Path,
) -> Result<&'a mut Replica<SqliteStorage>, StepError> {
    let replica = match slot.take() {
        Some(replica) => replica,
        None => {
            fs::create_dir_all(dir)
                .map_err(|e| step(e, "could not open the taskwarrior replica directory"))?;
            let storage = SqliteStorage::new(
                dir.join("taskchampion.sqlite3"),
                AccessMode::ReadWrite,
                true,
            )
            .await
            .map_err(|e| step(e, "could not open the taskwarrior replica directory"))?;
            Replica::new(storage)
        }
    };
    Ok(slot.insert(replica))
}

async fn open_server<'a>(
    slot: &'a mut Option<Box<dyn Server>>,
    config: &mut Option<ServerConfig>,
) -> Result<&'a mut Box<dyn Server>, StepError> {
    let server = match slot.take() {
        Some(server) => server,
        None => {
            let spent = config
                .take()
                .ok_or_else(|| StepError(SERVER_SPENT.to_string()))?;
            let retry = duplicate(&spent);
            match connect(spent).await {
                Ok(server) => server,
                Err(e) => {
                    *config = retry;
                    return Err(e);
                }
            }
        }
    };
    Ok(slot.insert(server))
}

async fn connect(config: ServerConfig) -> Result<Box<dyn Server>, StepError> {
    // The local server does not create its directory; create it as the replica's is.
    if let ServerConfig::Local { server_dir } = &config {
        fs::create_dir_all(server_dir)
            .map_err(|e| step(e, "could not open the taskwarrior server"))?;
    }
    config
        .into_server()
        .await
        .map_err(|e| step(e, "could not open the taskwarrior server"))
}

const SERVER_SPENT: &str =
    "the taskwarrior server failed to open and its configuration cannot be retried";

/// A copy of the configuration, kept so a failed open can be retried. `ServerConfig` is neither
/// `Clone` nor exhaustive; a variant this crate does not enable has no copy and opens once.
fn duplicate(config: &ServerConfig) -> Option<ServerConfig> {
    match config {
        ServerConfig::Local { server_dir } => Some(ServerConfig::Local {
            server_dir: server_dir.clone(),
        }),
        ServerConfig::Remote {
            url,
            client_id,
            encryption_secret,
        } => Some(ServerConfig::Remote {
            url: url.clone(),
            client_id: *client_id,
            encryption_secret: encryption_secret.clone(),
        }),
        _ => None,
    }
}

async fn sync(
    replica: &mut Replica<SqliteStorage>,
    server: &mut Box<dyn Server>,
) -> Result<(), StepError> {
    replica
        .sync(server, true)
        .await
        .map_err(|e| step(e, "taskwarrior sync failed"))
}

/// The one place a taskchampion or I/O error becomes a `StepError`. `what` is a fixed phrase;
/// nothing here interpolates a body or a configuration value.
fn step(e: impl Display, what: &str) -> StepError {
    StepError(format!("{what}: {e}"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[tokio::test]
    async fn a_panicking_worker_says_why_to_the_caller_and_to_every_later_request() {
        let dir = TempDir::new().unwrap();
        let worker = Taskwarrior::new(TaskwarriorConfig {
            replica_dir: dir.path().join("replica"),
            server: ServerConfig::Local {
                server_dir: dir.path().join("server"),
            },
        });
        let (reply, answer) = oneshot::channel();
        worker.requests.send(Request::Crash(reply)).unwrap();
        let first = answer.await.unwrap().unwrap_err();
        assert!(first.0.contains("boom"), "{}", first.0);
        for _ in 0..2 {
            let later = worker.tasks().await.unwrap_err();
            assert!(later.0.contains("boom"), "{}", later.0);
        }
    }
}

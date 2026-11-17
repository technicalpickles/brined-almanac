//! host-state's recovery close through almanac's runtime, the router and a real taskwarrior
//! replica, from reports to completed tasks.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use almanac::prelude::*;
use almanac::runtime::{Envelope, Origin, Runtime};
use almanac::store::MemoryStore;
use almanac::workflow::Registered;
use brined_almanac::host_state::{Episodes, Hosts};
use brined_almanac::providers::LogTracker;
use brined_almanac::routing::Router;
use brined_almanac::taskwarrior::{KEY_ATTR, Taskwarrior, TaskwarriorConfig};
use brined_almanac::workflows::host_state::{HostState, scan_findings};
use serde_json::{Value, json};
use taskchampion::ServerConfig;
use time::macros::datetime;
use time::{Duration, OffsetDateTime};

fn hosts() -> Hosts {
    Hosts::parse("[[host]]\nname = \"box\"\nproject = \"homelab\"\n").unwrap()
}

fn taskwarrior(dir: &Path, name: &str) -> Taskwarrior {
    Taskwarrior::new(TaskwarriorConfig {
        replica_dir: dir.join(name),
        server: ServerConfig::Local {
            server_dir: dir.join("server"),
        },
    })
}

fn rfc(t: OffsetDateTime) -> String {
    t.format(&time::format_description::well_known::Rfc3339)
        .unwrap()
}

fn report(generated_at: OffsetDateTime, checks: Value) -> String {
    json!({"host": "box", "generated_at": rfc(generated_at), "interval_seconds": 300, "checks": checks})
        .to_string()
}

/// The build before the Resolve effect, which keyed each run by its outage and only filed.
mod before_resolve {
    use almanac::prelude::*;
    use brined_almanac::workflows::host_state::{Facts, HostFinding, facts, policy};

    #[workflow(
        on = HostFinding,
        key = "{subject}",
        intent = "File one task for each host check that has been failing long enough.",
        may = [File],
        runs_per_hour = 1000,
        decide = files_only,
    )]
    pub async fn host_state(_cx: &Cx, event: &HostFinding) -> Result<Facts, Halt> {
        Ok(facts(event))
    }

    pub fn files_only(facts: &Facts) -> Vec<Effect> {
        policy(facts)
            .into_iter()
            .filter(|effect| matches!(effect, Effect::File { .. }))
            .collect()
    }
}

/// A daemon's worth of host-state: the runtime, its tracker's replica, and the scanner's memory.
struct Daemon {
    dir: tempfile::TempDir,
    store: Arc<MemoryStore>,
    tracker: Arc<dyn Tracker>,
    rt: Runtime,
    episodes: Episodes,
}

fn runtime(
    workflow: Box<dyn Registered>,
    tracker: &Arc<dyn Tracker>,
    store: &Arc<MemoryStore>,
) -> Runtime {
    Runtime::new(
        vec![workflow],
        Providers::new().provide::<dyn Tracker>(tracker.clone()),
    )
    .in_mode(Mode::Live)
    .with_store(store.clone())
}

impl Daemon {
    fn new() -> Self {
        Self::running(Box::new(HostState))
    }

    fn running(workflow: Box<dyn Registered>) -> Self {
        let dir = tempfile::tempdir().unwrap();
        std::fs::create_dir(dir.path().join("reports")).unwrap();
        let tracker: Arc<dyn Tracker> =
            Arc::new(Router::new(LogTracker, taskwarrior(dir.path(), "almanac")));
        let store = Arc::new(MemoryStore::default());
        let rt = runtime(workflow, &tracker, &store);
        Self {
            dir,
            store,
            tracker,
            rt,
            episodes: Episodes::default(),
        }
    }

    /// Restarts on the current build, keeping the store and the tracker.
    fn deploy(&mut self) {
        self.rt = runtime(Box::new(HostState), &self.tracker, &self.store);
        self.episodes = Episodes::default();
    }

    fn reports(&self) -> PathBuf {
        self.dir.path().join("reports")
    }

    /// Writes `report` and scans at `now`, as if the last scan was 15 minutes ago.
    fn findings(&mut self, now: OffsetDateTime, report: String) -> Vec<(String, Value)> {
        std::fs::write(self.reports().join("box.json"), report).unwrap();
        let previous = Some(now - Duration::minutes(15));
        scan_findings(&hosts(), &self.reports(), now, previous, &mut self.episodes)
    }

    async fn ingest(&self, found: Vec<(String, Value)>) {
        for (delivery_id, payload) in found {
            let envelope = Envelope {
                origin: Origin::Scan,
                source: "host-state".into(),
                delivery_id,
                depth: 0,
                kind: "host.state.finding".into(),
                payload,
            };
            self.rt.ingest(envelope).await.unwrap();
        }
    }

    async fn settle(&self) {
        self.rt.drain().await.unwrap();
        for delivery in self.rt.deliver().await.unwrap() {
            assert_eq!(delivery.error, None, "{}", delivery.key);
        }
    }

    async fn scan(&mut self, now: OffsetDateTime, report: String) {
        let found = self.findings(now, report);
        self.ingest(found).await;
        self.settle().await;
    }

    /// Each task's description and status, as another replica syncs them.
    async fn tasks(&self) -> Vec<(String, String)> {
        let mac = taskwarrior(self.dir.path(), "mac");
        mac.sync().await.unwrap();
        let mut tasks: Vec<_> = mac
            .tasks()
            .await
            .unwrap()
            .into_iter()
            .map(|task| {
                assert!(task.get(KEY_ATTR).is_some());
                (
                    task.get("description").unwrap().to_string(),
                    task.get("status").unwrap().to_string(),
                )
            })
            .collect();
        tasks.sort();
        tasks
    }
}

fn status_of<'a>(tasks: &'a [(String, String)], needle: &str) -> &'a str {
    &tasks
        .iter()
        .find(|(description, _)| description.contains(needle))
        .unwrap_or_else(|| panic!("no task for {needle} in {tasks:?}"))
        .1
}

#[tokio::test]
async fn a_recovered_check_completes_its_task_and_leaves_another_checks_open() {
    let mut daemon = Daemon::new();
    let start = datetime!(2026-11-11 12:00 UTC);
    let broke = "2026-11-11T11:00:00Z";
    let healed = "2026-11-11T12:30:00Z";
    daemon
        .scan(
            start,
            report(
                start,
                json!([
                    {"name": "timer", "status": 2, "since": broke, "output": "failed"},
                    {"name": "site", "status": 2, "since": broke, "output": "503"},
                ]),
            ),
        )
        .await;
    let later = start + Duration::minutes(105);
    daemon
        .scan(
            later,
            report(
                later,
                json!([
                    {"name": "timer", "status": 0, "since": healed, "output": ""},
                    {"name": "site", "status": 2, "since": broke, "output": "503"},
                ]),
            ),
        )
        .await;
    let tasks = daemon.tasks().await;
    assert_eq!(tasks.len(), 2);
    assert_eq!(status_of(&tasks, "timer"), "completed");
    assert_eq!(status_of(&tasks, "site"), "pending");
}

#[tokio::test]
async fn a_host_whose_reports_resume_completes_its_stale_task() {
    let mut daemon = Daemon::new();
    let healthy =
        json!([{"name": "timer", "status": 0, "since": "2026-11-11T09:00:00Z", "output": ""}]);
    let start = datetime!(2026-11-11 12:00 UTC);
    let last = start - Duration::minutes(45);
    daemon.scan(start, report(last, healthy.clone())).await;
    assert_eq!(
        status_of(&daemon.tasks().await, "no report from box"),
        "pending"
    );
    for minutes in [15, 30, 45, 60, 75] {
        let now = start + Duration::minutes(minutes);
        daemon.scan(now, report(now, healthy.clone())).await;
    }
    assert_eq!(
        status_of(&daemon.tasks().await, "no report from box"),
        "pending",
        "current for exactly the hold"
    );
    let now = start + Duration::minutes(90);
    daemon.scan(now, report(now, healthy)).await;
    let tasks = daemon.tasks().await;
    assert_eq!(tasks.len(), 1);
    assert_eq!(status_of(&tasks, "no report from box"), "completed");
}

/// A key's next event waits for its last run's effects, and a newer pending event replaces an
/// older one. So a recovery can be lost when a new outage arrives before it runs, and the next
/// recovery has to close both outages' tasks.
#[tokio::test]
async fn a_recovery_lost_to_a_newer_outage_is_made_up_by_the_next() {
    let mut daemon = Daemon::new();
    let start = datetime!(2026-11-11 12:00 UTC);
    let check = |status: i64, since: &str| json!([{"name": "timer", "status": status, "since": since, "output": ""}]);
    daemon
        .scan(start, report(start, check(2, "2026-11-11T11:00:00Z")))
        .await;
    let healed_at = start + Duration::minutes(90);
    let recovery = daemon.findings(
        healed_at,
        report(healed_at, check(0, "2026-11-11T12:15:00Z")),
    );
    let broke_at = start + Duration::minutes(180);
    let new_outage = daemon.findings(broke_at, report(broke_at, check(2, "2026-11-11T14:00:00Z")));
    assert!(recovery.iter().any(|(_, p)| p["kind"] == "recovered"));
    assert!(new_outage.iter().any(|(_, p)| p["kind"] == "check"));
    daemon.ingest(recovery).await;
    daemon.ingest(new_outage).await;
    daemon.settle().await;
    let tasks = daemon.tasks().await;
    assert_eq!(tasks.len(), 2, "{tasks:?}");
    assert!(
        tasks.iter().all(|(_, status)| status == "pending"),
        "the recovery was coalesced away: {tasks:?}"
    );
    let healed_again = start + Duration::minutes(330);
    daemon
        .scan(
            healed_again,
            report(healed_again, check(0, "2026-11-11T15:00:00Z")),
        )
        .await;
    let tasks = daemon.tasks().await;
    assert!(
        tasks.iter().all(|(_, status)| status == "completed"),
        "{tasks:?}"
    );
}

#[tokio::test]
async fn a_task_filed_before_resolve_stays_open_until_closed_by_hand() {
    let mut daemon = Daemon::running(Box::new(before_resolve::HostState));
    let check = |status: i64, since: &str| json!([{"name": "timer", "status": status, "since": since, "output": ""}]);
    let start = datetime!(2026-11-11 12:00 UTC);
    daemon
        .scan(start, report(start, check(2, "2026-11-11T11:00:00Z")))
        .await;
    daemon.deploy();
    let after = start + Duration::minutes(15);
    daemon
        .scan(after, report(after, check(2, "2026-11-11T11:00:00Z")))
        .await;
    assert_eq!(
        daemon.tasks().await.len(),
        1,
        "the outage's delivery ID was already seen, so the new build files nothing for it"
    );
    for minutes in [60, 75, 90, 105, 120] {
        let now = start + Duration::minutes(minutes);
        daemon
            .scan(now, report(now, check(0, "2026-11-11T12:30:00Z")))
            .await;
    }
    let tasks = daemon.tasks().await;
    assert_eq!(
        tasks
            .iter()
            .map(|(_, status)| status.as_str())
            .collect::<Vec<_>>(),
        ["pending"],
        "the recovery resolves what <host>.<check> filed, and the old run was keyed by outage"
    );
    let broke = start + Duration::minutes(180);
    daemon
        .scan(broke, report(broke, check(2, "2026-11-11T14:45:00Z")))
        .await;
    let healed = start + Duration::minutes(300);
    daemon
        .scan(healed, report(healed, check(0, "2026-11-11T15:30:00Z")))
        .await;
    let tasks = daemon.tasks().await;
    assert_eq!(tasks.len(), 2, "{tasks:?}");
    assert_eq!(status_of(&tasks, "since 2026-11-11 11:00Z"), "pending");
    assert_eq!(status_of(&tasks, "since 2026-11-11 14:45Z"), "completed");
}

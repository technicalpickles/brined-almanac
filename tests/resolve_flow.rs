//! A `Resolve` of filed work through almanac's runtime, the router and a real taskwarrior replica.

use std::path::Path;
use std::sync::Arc;

use almanac::prelude::*;
use almanac::runtime::{Envelope, Origin, Runtime};
use almanac::store::MemoryStore;
use brined_almanac::providers::LogTracker;
use brined_almanac::routing::Router;
use brined_almanac::taskwarrior::{KEY_ATTR, Taskwarrior, TaskwarriorConfig};
use brined_almanac::workflows::host_state::HostFinding;
use serde_json::json;
use taskchampion::ServerConfig;

clean! {
    pub struct Seen { pub target: Slug, pub subject: Slug, pub check: Slug, pub ok: bool }
}

/// host-state's shape with a recovery close: a failing check files, the same outage back to ok
/// closes what was filed for it.
#[workflow(on = HostFinding, key = "{subject}", intent = "x", may = [File, Resolve], runs_per_hour = 9, decide = file_or_close)]
async fn closes_on_recovery(_cx: &Cx, event: &HostFinding) -> Result<Seen, Halt> {
    let target: Slug = format!("task:{}", event.project).parse().unwrap();
    Ok(Seen {
        target,
        subject: event.subject.clone(),
        check: event.check.clone().unwrap(),
        ok: event.status.as_ref().is_some_and(|s| s.to_string() == "ok"),
    })
}

fn file_or_close(seen: &Seen) -> Vec<Effect> {
    if seen.ok {
        vec![Effect::Resolve {
            target: seen.target.clone(),
            item: Item::Filed {
                key: seen.subject.to_string(),
            },
            evidence: title!("{} is ok again", seen.check),
        }]
    } else {
        vec![Effect::File {
            title: title!("{} is critical", seen.check),
            target: seen.target.clone(),
            body: Untrusted::new(String::new()),
        }]
    }
}

fn finding(delivery: &str, subject: &str, status: &str) -> Envelope {
    Envelope {
        origin: Origin::Webhook,
        source: "host-state".into(),
        delivery_id: delivery.into(),
        depth: 0,
        kind: "host.state.finding".into(),
        payload: json!({
            "kind": "check", "project": "homelab", "subject": subject, "host": "box",
            "check": "timer-sync", "status": status, "since": "2026-11-11T12:29Z", "body": "",
        }),
    }
}

fn taskwarrior(dir: &Path, name: &str) -> Taskwarrior {
    Taskwarrior::new(TaskwarriorConfig {
        replica_dir: dir.join(name),
        server: ServerConfig::Local {
            server_dir: dir.join("server"),
        },
    })
}

#[tokio::test]
async fn a_recovered_outage_completes_the_task_filed_for_it_and_no_other() {
    let dir = tempfile::tempdir().unwrap();
    let tracker: Arc<dyn Tracker> =
        Arc::new(Router::new(LogTracker, taskwarrior(dir.path(), "almanac")));
    let rt = Runtime::new(
        vec![Box::new(ClosesOnRecovery)],
        Providers::new().provide::<dyn Tracker>(tracker),
    )
    .in_mode(Mode::Live)
    .with_store(Arc::new(MemoryStore::default()));
    for envelope in [
        finding("d1", "box.timer-sync.2026-11-11T12:29Z", "critical"),
        finding("d2", "box.timer-sync.2026-11-09T08:00Z", "critical"),
        finding("d3", "box.timer-sync.2026-11-11T12:29Z", "ok"),
    ] {
        rt.ingest(envelope).await.unwrap();
        rt.drain().await.unwrap();
        for delivery in rt.deliver().await.unwrap() {
            assert_eq!(delivery.error, None, "{}", delivery.key);
        }
    }
    let mac = taskwarrior(dir.path(), "mac");
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    let mut statuses: Vec<_> = tasks
        .iter()
        .map(|t| (t.get(KEY_ATTR).unwrap(), t.get("status").unwrap()))
        .collect();
    statuses.sort();
    assert_eq!(statuses.len(), 2, "{statuses:?}");
    assert_eq!(
        statuses.iter().map(|(_, s)| *s).collect::<Vec<_>>(),
        ["completed", "pending"],
        "the first outage's task closes, the older outage's stays open: {statuses:?}"
    );
}

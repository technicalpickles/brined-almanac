//! host-state's recovery close through almanac's runtime, the router and a real taskwarrior
//! replica, from reports to completed tasks.

use std::path::Path;
use std::sync::Arc;

use almanac::prelude::*;
use almanac::runtime::{Envelope, Origin, Runtime};
use almanac::store::MemoryStore;
use brined_almanac::host_state::{Episodes, Hosts};
use brined_almanac::providers::LogTracker;
use brined_almanac::routing::Router;
use brined_almanac::taskwarrior::{KEY_ATTR, Taskwarrior, TaskwarriorConfig};
use brined_almanac::workflows::host_state::{HostState, scan_findings};
use serde_json::json;
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

fn report(now: OffsetDateTime, checks: serde_json::Value) -> String {
    let rfc = |t: OffsetDateTime| {
        t.format(&time::format_description::well_known::Rfc3339)
            .unwrap()
    };
    json!({"host": "box", "generated_at": rfc(now), "interval_seconds": 300, "checks": checks})
        .to_string()
}

#[tokio::test]
async fn a_recovered_check_completes_its_task_and_leaves_another_checks_open() {
    let dir = tempfile::tempdir().unwrap();
    let reports = dir.path().join("reports");
    std::fs::create_dir(&reports).unwrap();
    let tracker: Arc<dyn Tracker> =
        Arc::new(Router::new(LogTracker, taskwarrior(dir.path(), "almanac")));
    let rt = Runtime::new(
        vec![Box::new(HostState)],
        Providers::new().provide::<dyn Tracker>(tracker),
    )
    .in_mode(Mode::Live)
    .with_store(Arc::new(MemoryStore::default()));
    let start = datetime!(2026-11-11 12:00 UTC);
    let broke = "2026-11-11T11:00:00Z";
    let healed = "2026-11-11T12:30:00Z";
    let mut episodes = Episodes::default();
    let scans = [
        (
            start,
            json!([
                {"name": "timer", "status": 2, "since": broke, "output": "failed"},
                {"name": "site", "status": 2, "since": broke, "output": "503"},
            ]),
        ),
        (
            start + Duration::minutes(105),
            json!([
                {"name": "timer", "status": 0, "since": healed, "output": ""},
                {"name": "site", "status": 2, "since": broke, "output": "503"},
            ]),
        ),
    ];
    for (now, checks) in scans {
        std::fs::write(reports.join("box.json"), report(now, checks)).unwrap();
        let previous = Some(now - Duration::minutes(15));
        for (id, payload) in scan_findings(&hosts(), &reports, now, previous, &mut episodes) {
            let envelope = Envelope {
                origin: Origin::Scan,
                source: "host-state".into(),
                delivery_id: id,
                depth: 0,
                kind: "host.state.finding".into(),
                payload,
            };
            rt.ingest(envelope).await.unwrap();
        }
        rt.drain().await.unwrap();
        for delivery in rt.deliver().await.unwrap() {
            assert_eq!(delivery.error, None, "{}", delivery.key);
        }
    }
    let mac = taskwarrior(dir.path(), "mac");
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    let status_of = |check: &str| {
        let task = tasks
            .iter()
            .find(|t| t.get("description").is_some_and(|d| d.contains(check)))
            .unwrap_or_else(|| panic!("no task for {check}"));
        assert!(task.get(KEY_ATTR).is_some());
        task.get("status").unwrap().to_string()
    };
    assert_eq!(tasks.len(), 2);
    assert_eq!(status_of("timer"), "completed");
    assert_eq!(status_of("site"), "pending");
}

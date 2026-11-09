//! Files one probe task into the real taskchampion history, to prove another client can read what
//! this code writes. Ignored by default because it writes to live data. Run it with the secrets
//! `scripts/live-interop` injects, then `task sync` on a Mac and look for `+almanac`.

use almanac::prelude::*;
use brined_almanac::providers::{Choice, TasksSide, choose};
use brined_almanac::taskwarrior::Taskwarrior;
use tempfile::TempDir;

#[tokio::test]
#[ignore = "writes one task to the live taskchampion history"]
async fn file_a_probe_task_into_the_live_history() {
    let lookup = |key: &str| std::env::var(key).ok().filter(|v| !v.is_empty());
    let Ok(Choice::Route {
        tasks: TasksSide::Taskwarrior(mut config),
        ..
    }) = choose(lookup)
    else {
        panic!("TASKCHAMPION_SYNC_URL, _CLIENT_ID and _ENCRYPTION_SECRET must be set");
    };
    let dir = TempDir::new().unwrap();
    config.replica_dir = dir.path().join("replica");
    let key = format!(
        "probe:{}",
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs()
    );
    let target: Slug = "task:brined-almanac".parse().unwrap();
    let body = Untrusted::new(
        "Filed by brined-almanac's live interop probe to prove the Mac can read it. Safe to delete."
            .to_string(),
    );
    Taskwarrior::new(config)
        .file(
            &key,
            &target,
            "almanac interop probe (safe to delete)",
            &body,
        )
        .await
        .unwrap();
    println!("filed {key}");
}

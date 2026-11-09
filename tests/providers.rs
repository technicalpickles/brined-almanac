use std::collections::HashMap;
use std::path::Path;

use brined_almanac::providers::{Choice, IssuesSide, TasksSide, choose};
use taskchampion::{ServerConfig, Uuid};

const CLIENT_ID: &str = "6f1d2c3b-4a5e-4f60-8172-839a4b5c6d7e";

fn with(pairs: &[(&'static str, &'static str)]) -> Result<Choice, String> {
    let map: HashMap<&str, &str> = pairs.iter().copied().collect();
    choose(|k| map.get(k).map(|v| v.to_string()))
}

fn taskchampion() -> Vec<(&'static str, &'static str)> {
    vec![
        ("TASKCHAMPION_SYNC_URL", "https://sync.example.test"),
        ("TASKCHAMPION_CLIENT_ID", CLIENT_ID),
        ("TASKCHAMPION_ENCRYPTION_SECRET", "s3cret-as-stored"),
        ("TASKCHAMPION_REPLICA_DIR", "/some/dir"),
    ]
}

fn without(
    mut pairs: Vec<(&'static str, &'static str)>,
    key: &str,
) -> Vec<(&'static str, &'static str)> {
    pairs.retain(|(k, _)| *k != key);
    pairs
}

#[test]
fn no_database_means_store_required_for_both_sides() {
    let mut env = taskchampion();
    env.push(("GITHUB_TOKEN", "a-token"));
    assert!(matches!(with(&env).unwrap(), Choice::StoreRequired));
}

#[test]
fn no_sync_url_means_log_on_the_tasks_side() {
    let choice = with(&[
        ("DATABASE_URL", "postgres://db"),
        ("GITHUB_TOKEN", "a-token"),
    ])
    .unwrap();
    assert!(matches!(
        choice,
        Choice::Route { issues: IssuesSide::Github { token }, tasks: TasksSide::Log } if token == "a-token"
    ));
}

#[test]
fn no_token_means_log_on_the_issues_side() {
    let mut env = taskchampion();
    env.push(("DATABASE_URL", "postgres://db"));
    assert!(matches!(
        with(&env).unwrap(),
        Choice::Route {
            issues: IssuesSide::Log,
            tasks: TasksSide::Taskwarrior(_)
        }
    ));
}

#[test]
fn a_full_configuration_builds_both() {
    let mut env = taskchampion();
    env.push(("DATABASE_URL", "postgres://db"));
    env.push(("GITHUB_TOKEN", "a-token"));
    let Choice::Route {
        issues,
        tasks: TasksSide::Taskwarrior(config),
    } = with(&env).unwrap()
    else {
        panic!("expected a route with a taskwarrior tasks side");
    };
    assert!(matches!(issues, IssuesSide::Github { token } if token == "a-token"));
    assert_eq!(config.replica_dir, Path::new("/some/dir"));
    let expected_id: Uuid = CLIENT_ID.parse().unwrap();
    assert!(matches!(
        config.server,
        ServerConfig::Remote { url, client_id, encryption_secret }
            if url == "https://sync.example.test"
                && client_id == expected_id
                && encryption_secret == b"s3cret-as-stored".to_vec()
    ));
}

#[test]
fn a_url_without_a_client_id_is_an_error_naming_it() {
    let err = with(&without(taskchampion(), "TASKCHAMPION_CLIENT_ID")).unwrap_err();
    assert!(err.contains("TASKCHAMPION_CLIENT_ID"));
    assert!(!err.contains("s3cret"));
}

#[test]
fn a_url_without_a_secret_is_an_error_naming_it() {
    let err = with(&without(taskchampion(), "TASKCHAMPION_ENCRYPTION_SECRET")).unwrap_err();
    assert!(err.contains("TASKCHAMPION_ENCRYPTION_SECRET"));
}

#[test]
fn a_client_id_that_is_not_a_uuid_is_an_error_naming_it() {
    let mut env = without(taskchampion(), "TASKCHAMPION_CLIENT_ID");
    env.push(("TASKCHAMPION_CLIENT_ID", "not-a-uuid"));
    let err = with(&env).unwrap_err();
    assert!(err.contains("TASKCHAMPION_CLIENT_ID"));
    assert!(!err.contains("not-a-uuid"));
    assert!(!err.contains("s3cret"));
}

#[test]
fn the_replica_dir_defaults() {
    let mut env = without(taskchampion(), "TASKCHAMPION_REPLICA_DIR");
    env.push(("DATABASE_URL", "postgres://db"));
    let Choice::Route {
        tasks: TasksSide::Taskwarrior(config),
        ..
    } = with(&env).unwrap()
    else {
        panic!("expected a route with a taskwarrior tasks side");
    };
    assert_eq!(
        config.replica_dir,
        Path::new("/var/lib/brined-almanac/taskchampion")
    );
}

#[test]
fn a_client_id_without_a_url_is_an_error_naming_both() {
    let err = with(&without(taskchampion(), "TASKCHAMPION_SYNC_URL")).unwrap_err();
    assert_eq!(
        err,
        "TASKCHAMPION_CLIENT_ID is set but TASKCHAMPION_SYNC_URL is not"
    );
}

#[test]
fn a_token_without_a_database_or_tasks_means_store_required() {
    assert!(matches!(
        with(&[("GITHUB_TOKEN", "a-token")]).unwrap(),
        Choice::StoreRequired
    ));
}

#[test]
fn nothing_set_prints_both_sides() {
    assert!(matches!(
        with(&[]).unwrap(),
        Choice::Route {
            issues: IssuesSide::Log,
            tasks: TasksSide::Log
        }
    ));
}

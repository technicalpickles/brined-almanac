use std::process::ExitCode;
use std::{env, fs};

use brined_almanac::{cutover, providers, taskwarrior, workflows};

fn main() -> ExitCode {
    match env::args().nth(1).as_deref() {
        Some("sync-check") => return sync_check(),
        Some("cutover") => return cutover(),
        _ => {}
    }
    almanac::main("brined-almanac", workflows::all, providers::all)
}

/// Pulls the real server's history into a throwaway replica, so a wrong URL, client id or
/// encryption secret fails here instead of at the first filing.
fn sync_check() -> ExitCode {
    let dir = env::temp_dir().join(format!("brined-almanac-sync-check-{}", std::process::id()));
    let outcome =
        providers::sync_check_config(|key| env::var(key).ok(), dir.clone()).and_then(|config| {
            let runtime = tokio::runtime::Builder::new_current_thread()
                .build()
                .map_err(|err| err.to_string())?;
            runtime.block_on(taskwarrior::sync_check(config))
        });
    let _ = fs::remove_dir_all(&dir);
    match outcome {
        Ok(pulled) => {
            println!("brined-almanac: sync ok, pulled {pulled} task(s) from the server");
            ExitCode::SUCCESS
        }
        Err(err) => {
            eprintln!("brined-almanac: sync-check failed: {err}");
            ExitCode::FAILURE
        }
    }
}

/// Resolves history in Bugsink: `cutover` prints the plan, `cutover --apply` carries it out.
fn cutover() -> ExitCode {
    let apply = match env::args().nth(2).as_deref() {
        None => false,
        Some("--apply") => true,
        Some(other) => {
            eprintln!("brined-almanac: cutover takes only --apply, not {other}");
            return ExitCode::FAILURE;
        }
    };
    let outcome = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|err| err.to_string())
        .and_then(|runtime| runtime.block_on(cutover::run(|key| env::var(key).ok(), apply)));
    match outcome {
        Ok(()) => ExitCode::SUCCESS,
        Err(err) => {
            eprintln!("brined-almanac: cutover failed: {err}");
            ExitCode::FAILURE
        }
    }
}

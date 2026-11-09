use std::process::ExitCode;
use std::{env, fs};

use brined_almanac::{providers, taskwarrior, workflows};

fn main() -> ExitCode {
    if env::args().nth(1).as_deref() == Some("sync-check") {
        return sync_check();
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

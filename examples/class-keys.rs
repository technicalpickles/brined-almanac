use std::collections::BTreeMap;
use std::env;
use std::path::PathBuf;
use std::process::ExitCode;

use brined_almanac::classes::classify;
use brined_almanac::transcript::read;

fn main() -> ExitCode {
    let Some(dir) = env::args().nth(1).map(PathBuf::from) else {
        eprintln!("usage: class-keys <transcripts-dir>");
        return ExitCode::FAILURE;
    };
    let scan = match read(&dir) {
        Ok(scan) => scan,
        Err(e) => {
            eprintln!("{e}");
            return ExitCode::FAILURE;
        }
    };
    let mut keys: BTreeMap<(String, String, &str), u64> = BTreeMap::new();
    for e in &scan.errors {
        let class = e.text.narrow(|t| classify(t).name);
        *keys
            .entry((e.service.to_string(), e.tool.to_string(), class))
            .or_default() += 1;
    }
    for ((service, tool, class), count) in keys {
        println!("{service}\t{tool}\t{class}\t{count}");
    }
    ExitCode::SUCCESS
}

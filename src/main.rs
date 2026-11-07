use brined_almanac::{providers, workflows};

fn main() -> std::process::ExitCode {
    almanac::main("brined-almanac", workflows::all, providers::all)
}

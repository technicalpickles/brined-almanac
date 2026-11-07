use std::env;
use std::path::PathBuf;
use std::sync::Arc;

use almanac::prelude::*;

use crate::transcript::Transcripts;

pub fn all(_config: &Config) -> Providers {
    let dir = env::var("BRENT_TRANSCRIPTS").unwrap_or_else(|_| "transcripts".to_string());
    Providers::new().provide::<Transcripts>(Arc::new(Transcripts(PathBuf::from(dir))))
}

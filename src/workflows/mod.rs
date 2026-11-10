use almanac::prelude::*;

pub mod bugsink_issues;
pub mod host_state;
pub mod security_alerts;

pub fn all() -> Vec<Box<dyn Registered>> {
    vec![
        // A backfill scans in this order and stops at the first scan that errors. The other two
        // scans error on the same network trouble that stops the puller, so host-state, which
        // reads local files and reports that trouble, goes first.
        Box::new(host_state::HostState),
        Box::new(bugsink_issues::BugsinkIssues),
        Box::new(security_alerts::SecurityAlerts),
    ]
}

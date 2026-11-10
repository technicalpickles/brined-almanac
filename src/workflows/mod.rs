use almanac::prelude::*;

pub mod bugsink_issues;
pub mod security_alerts;

pub fn all() -> Vec<Box<dyn Registered>> {
    vec![
        Box::new(bugsink_issues::BugsinkIssues),
        Box::new(security_alerts::SecurityAlerts),
    ]
}

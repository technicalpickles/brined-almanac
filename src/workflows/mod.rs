use almanac::prelude::*;

pub mod security_alerts;
pub mod tool_failures;

pub fn all() -> Vec<Box<dyn Registered>> {
    vec![Box::new(tool_failures::ToolFailures)]
}

use almanac::prelude::*;
use serde::{Deserialize, Serialize};
use serde_json::json;

use crate::bugsink::ship_from;
use crate::bugsink_api::{Issue, Labels, generation, labels};
use crate::classes::{class_named, owner_of};
use crate::cutover;
use crate::transcript::{Transcripts, read};

/// Always provided, because almanac checks a scan's providers at startup. `None` means no API
/// token or no DSN.
pub struct BugsinkReader(pub Option<cutover::Bugsink>);

clean! {
    pub struct Surface {
        pub name: Slug,
        pub count: u64,
    }
}

observed! {
    /// A Bugsink issue that should have a GitHub issue. `generation` counts the times it came
    /// back after being resolved. `reason` is transcript text, so it is tainted.
    pub struct Unaddressed {
        pub issue: Slug,
        pub generation: u64,
        pub service: Slug,
        pub tool: Slug,
        pub class: Slug,
        pub count: u64,
        pub first_seen: Slug,
        pub last_seen: Slug,
        pub surfaces: Vec<Surface>,
        pub sessions: u64,
        pub link: Slug,
        pub reason: Untrusted<String>,
    }
}

impl Event for Unaddressed {
    const KIND: &'static str = "bugsink.issue.unaddressed";
    fn parse(p: &Payload<'_>) -> Result<Self, Malformed> {
        Ok(Self {
            issue: p.field("/issue")?,
            generation: p.field("/generation")?,
            service: p.field("/service")?,
            tool: p.field("/tool")?,
            class: p.field("/class")?,
            count: p.field("/count")?,
            first_seen: p.field("/first_seen")?,
            last_seen: p.field("/last_seen")?,
            surfaces: p.field("/surfaces")?,
            sessions: p.field("/sessions")?,
            link: p.field("/link")?,
            reason: p.text("/reason")?,
        })
    }
}

/// An issue wants a GitHub issue when nobody has resolved or muted it and it has as many events
/// as its class needs.
pub fn unaddressed(issue: &Issue, class: &str) -> bool {
    !issue.resolved && !issue.muted && issue.count >= class_named(class).threshold
}

fn scanned(issue: &Issue, labels: &Labels, generation: u64, link: String) -> Scanned<Unaddressed> {
    let surfaces: Vec<_> = labels
        .surfaces
        .iter()
        .map(|(name, count)| json!({"name": name, "count": count}))
        .collect();
    let payload = json!({
        "issue": issue.id,
        "generation": generation,
        "service": labels.service,
        "tool": labels.tool,
        "class": labels.class,
        "count": issue.count,
        "first_seen": issue.first_seen,
        "last_seen": issue.last_seen,
        "surfaces": surfaces,
        "sessions": labels.sessions,
        "link": link,
        "reason": issue.reason,
    });
    Scanned::new(format!("bugsink:{}", issue.id), payload).at_state(generation)
}

#[scan]
pub async fn scan(scanner: &Scanner) -> Result<Vec<Scanned<Unaddressed>>, StepError> {
    let dir = scanner.get::<Transcripts>()?;
    let read = read(&dir.0)?;
    let shown = dir.0.display();
    if read.skipped.not_json > 0 {
        eprintln!(
            "{shown}: skipped {} lines that were not JSON",
            read.skipped.not_json
        );
    }
    if read.skipped.undated > 0 {
        eprintln!(
            "{shown}: skipped {} lines with no usable timestamp",
            read.skipped.undated
        );
    }
    ship_from(scanner, &read.errors).await?;

    let Some(bugsink) = &scanner.get::<BugsinkReader>()?.0 else {
        eprintln!("bugsink-issues: BUGSINK_API_TOKEN or BUGSINK_DSN is not set, skipping");
        return Ok(vec![]);
    };
    let failed = StepError;
    let mut found = vec![];
    for issue in bugsink.open_issues().await.map_err(failed)? {
        if issue.resolved || issue.muted {
            continue;
        }
        let tags = bugsink.tags(&issue).await.map_err(failed)?;
        let labels = match labels(&tags) {
            Ok(labels) => labels,
            Err(why) => {
                eprintln!("bugsink-issues: skipping {}: {why}", issue.friendly_id);
                continue;
            }
        };
        if !unaddressed(&issue, &labels.class) {
            continue;
        }
        let history = bugsink.history(&issue).await.map_err(failed)?;
        let link = bugsink.issue_url(&issue);
        found.push(scanned(&issue, &labels, generation(&history), link));
    }
    Ok(found)
}

#[derive(Serialize, Deserialize)]
pub struct Facts {
    pub target: Slug,
    pub service: Slug,
    pub tool: Slug,
    pub class: Slug,
    pub count: u64,
    pub generation: u64,
    pub body: Untrusted<String>,
}

pub fn facts(event: &Unaddressed) -> Facts {
    let header = format!(
        "service: {}\ntool: {}\nclass: {}\n\nreason:\n",
        event.service, event.tool, event.class
    );
    Facts {
        target: owner_of(&event.service),
        service: event.service.clone(),
        tool: event.tool.clone(),
        class: event.class.clone(),
        count: event.count,
        generation: event.generation,
        body: Untrusted::join(&header, &event.reason),
    }
}

#[workflow(
    on = Unaddressed,
    key = "bugsink:{issue}:{generation}",
    intent = "File one issue per Bugsink issue of failed tool calls that nobody has resolved or muted.",
    may = [File],
    runs_per_hour = 1000,
    scan = scan,
    decide = policy,
)]
pub async fn bugsink_issues(_cx: &Cx, event: &Unaddressed) -> Result<Facts, Halt> {
    Ok(facts(event))
}

pub fn policy(facts: &Facts) -> Vec<Effect> {
    vec![Effect::File {
        title: title!(
            "Brent: {}/{} {} ({} events)",
            facts.service,
            facts.tool,
            facts.class,
            facts.count
        ),
        target: facts.target.clone(),
        body: facts.body.clone(),
    }]
}

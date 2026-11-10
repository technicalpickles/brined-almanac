use std::collections::BTreeSet;

use almanac::prelude::*;
use serde::{Deserialize, Serialize};
use serde_json::json;

use crate::bugsink::ship_from;
use crate::bugsink_api::{Issue, Labels, generation, labels, last_unmuted, seen};
use crate::classes::{class_named, owner_of};
use crate::cutover::{self, REPOS};
use crate::resolve_on_close::{
    MUTE_WAKE_EVENTS, Open, Verdict, decide, mute_comment, resolve_comment,
};
use crate::transcript::{Transcripts, read};

/// Always provided, because almanac checks a scan's providers at startup. `None` means no API
/// token or no DSN.
pub struct BugsinkReader(pub Option<Reading>);

pub struct Reading {
    pub bugsink: cutover::Bugsink,
    /// `None` without `GITHUB_TOKEN`, which turns off acting on closed almanac issues.
    pub github: Option<cutover::Github>,
    /// A dry run reads and says what it would do, and changes nothing in Bugsink.
    pub live: bool,
}

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
        pub ingest_dated: bool,
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
            ingest_dated: p.field("/ingest_dated")?,
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

fn scanned(
    issue: &Issue,
    labels: &Labels,
    generation: u64,
    (first_seen, last_seen, ingest_dated): (String, String, bool),
    link: String,
) -> Scanned<Unaddressed> {
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
        "first_seen": first_seen,
        "last_seen": last_seen,
        "ingest_dated": ingest_dated,
        "surfaces": surfaces,
        "sessions": labels.sessions,
        "link": link,
        "reason": issue.reason,
    });
    Scanned::new(format!("bugsink:{}", issue.id), payload).at_state(generation)
}

/// Resolves or mutes each open Bugsink issue whose almanac issue was closed, so that the close
/// never hides the failure's next event. Returns the issues it settled, which the scan must not
/// report again. Every failure is printed and skipped: GitHub or Bugsink being down must not stop
/// the filing that follows.
async fn settle_closed(reading: &Reading, open: &[Issue]) -> BTreeSet<String> {
    let mut settled = BTreeSet::new();
    let Some(github) = &reading.github else {
        eprintln!("bugsink-issues: GITHUB_TOKEN is not set, not resolving closed issues");
        return settled;
    };
    let mut linked = vec![];
    for repo in REPOS {
        match github.linked(repo).await {
            Ok(found) => linked.extend(found),
            Err(why) => {
                eprintln!("bugsink-issues: not resolving closed issues: {why}");
                return settled;
            }
        }
    }
    let named: BTreeSet<&str> = linked.iter().map(|l| l.issue.as_str()).collect();
    for issue in open.iter().filter(|i| named.contains(i.id.as_str())) {
        let history = match reading.bugsink.history(issue).await {
            Ok(history) => history,
            Err(why) => {
                eprintln!("bugsink-issues: {why}");
                continue;
            }
        };
        let unmuted_at = match last_unmuted(&history) {
            Ok(at) => at,
            Err(why) => {
                eprintln!("bugsink-issues: {}: {why}", issue.friendly_id);
                continue;
            }
        };
        let state = Open {
            id: issue.id.clone(),
            generation: generation(&history),
            unmuted_at,
        };
        let name = &issue.friendly_id;
        let done = match decide(&state, &linked) {
            Verdict::Leave => continue,
            Verdict::Resolve { proof } if !reading.live => {
                eprintln!("bugsink-issues: dry run, would resolve {name} for {proof}");
                Ok(())
            }
            Verdict::Mute { proof } if !reading.live => {
                eprintln!("bugsink-issues: dry run, would mute {name} for {proof}");
                Ok(())
            }
            Verdict::Resolve { proof } => {
                reading
                    .bugsink
                    .resolve_issue(issue, resolve_comment(&proof))
                    .await
            }
            Verdict::Mute { proof } => {
                reading
                    .bugsink
                    .mute_until(issue, mute_comment(&proof), MUTE_WAKE_EVENTS)
                    .await
            }
        };
        settled.insert(issue.id.clone());
        match done {
            Ok(()) if reading.live => eprintln!("bugsink-issues: settled {name}"),
            Ok(()) => {}
            Err(why) => eprintln!("bugsink-issues: {why}"),
        }
    }
    settled
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

    let reader = scanner.get::<BugsinkReader>()?;
    let Some(reading) = &reader.0 else {
        eprintln!("bugsink-issues: BUGSINK_API_TOKEN or BUGSINK_DSN is not set, skipping");
        return Ok(vec![]);
    };
    let bugsink = &reading.bugsink;
    let failed = StepError;
    let open: Vec<Issue> = bugsink
        .open_issues()
        .await
        .map_err(failed)?
        .into_iter()
        .filter(|issue| !issue.resolved && !issue.muted)
        .collect();
    let settled = settle_closed(reading, &open).await;
    let mut found = vec![];
    for issue in open {
        if settled.contains(&issue.id) {
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
        let events = bugsink.events(&issue).await.map_err(failed)?;
        let when = match seen(&events) {
            Ok((first, last)) => (first, last, false),
            Err(why) => {
                eprintln!(
                    "bugsink-issues: {} has no event times ({why}), dating it by Bugsink ingest time",
                    issue.friendly_id
                );
                (issue.first_seen.clone(), issue.last_seen.clone(), true)
            }
        };
        let link = bugsink.issue_url(&issue);
        found.push(scanned(&issue, &labels, generation(&history), when, link));
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
    let surfaces: Vec<String> = event
        .surfaces
        .iter()
        .map(|s| format!("{} ({})", s.name, s.count))
        .collect();
    let basis = if event.ingest_dated {
        " (Bugsink ingest time)"
    } else {
        ""
    };
    let mut header = format!(
        "service: {}\ntool: {}\nclass: {}\nevents: {}\nfirst seen: {}{basis}\nlast seen: {}{basis}\nsurfaces: {}\nsessions: {}\n",
        event.service,
        event.tool,
        event.class,
        event.count,
        event.first_seen,
        event.last_seen,
        surfaces.join(", "),
        event.sessions,
    );
    if event.generation > 0 {
        header.push_str(&format!(
            "regressions: {} (came back after being resolved)\n",
            event.generation
        ));
    }
    header.push_str(&format!("bugsink: {}\n\nreason:\n", event.link));
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
    let Facts {
        target,
        service,
        tool,
        class,
        count,
        generation,
        body,
    } = facts;
    let title = if *generation > 0 {
        title!(
            "Brent: {}/{} {} regressed ({} events)",
            service,
            tool,
            class,
            count
        )
    } else {
        title!("Brent: {}/{} {} ({} events)", service, tool, class, count)
    };
    vec![Effect::File {
        title,
        target: target.clone(),
        body: body.clone(),
    }]
}

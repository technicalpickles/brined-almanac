//! The `cutover` verb: resolve in Bugsink every fingerprint whose almanac issues are all closed
//! on GitHub, with a comment that links them. A dry run unless `--apply`.
//! Plan: `docs/superpowers/plans/2026-11-09-cutover.md`.

use std::collections::{BTreeMap, BTreeSet};

use reqwest::{Client, RequestBuilder};
use serde::Deserialize;
use serde_json::{Value, json};

use crate::bugsink::{Dsn, TIMEOUT};

/// Bugsink joins a custom fingerprint's parts with this to make its grouping key.
const JOIN: &str = " ⋄ ";

/// Where the filer files: `classes::owner_of` sends brineworks to the first, all else to
/// the second.
pub const REPOS: [&str; 2] = ["technicalpickles/brineworks", "technicalpickles/pickleclaw"];

/// The grouping key Bugsink computes for the fingerprint `bugsink::event` sends.
pub fn grouping_key(service: &str, tool: &str, class: &str) -> String {
    [service, tool, class].join(JOIN)
}

/// The grouping key named by an almanac issue body. Only the header lines before the first blank
/// line count, so the transcript sample after it can never supply a field.
pub fn key_in_body(body: &str) -> Option<String> {
    let mut fields: BTreeMap<&str, &str> = BTreeMap::new();
    for line in body
        .lines()
        .skip_while(|l| l.trim_start().starts_with("```"))
        .take_while(|l| !l.trim().is_empty())
    {
        if let Some((name, value)) = line.split_once(": ") {
            fields.entry(name.trim()).or_insert(value.trim());
        }
    }
    let field = |name| fields.get(name).copied().filter(|v| !v.is_empty());
    Some(grouping_key(
        field("service")?,
        field("tool")?,
        field("class")?,
    ))
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum State {
    Open,
    Closed,
}

/// One almanac issue on GitHub.
#[derive(Clone, Debug)]
pub struct Filed {
    pub url: String,
    pub state: State,
    pub key: Option<String>,
}

/// One Bugsink issue.
#[derive(Clone, Debug)]
pub struct Tracked {
    pub id: String,
    pub name: String,
    pub keys: Vec<String>,
    pub resolved: bool,
    pub first_seen: String,
}

#[derive(Debug, PartialEq, Eq)]
pub enum Verdict {
    Resolve { proof: Vec<String> },
    Open { open: Vec<String> },
    NeverFiled,
    AlreadyResolved,
}

pub struct Plan<'a> {
    pub verdicts: Vec<(&'a Tracked, Verdict)>,
    pub unmatched: Vec<&'a Filed>,
}

/// Decides each Bugsink issue from the almanac issues that share one of its grouping keys.
pub fn plan<'a>(filed: &'a [Filed], tracked: &'a [Tracked]) -> Plan<'a> {
    let mut by_key: BTreeMap<&str, Vec<&Filed>> = BTreeMap::new();
    for f in filed {
        if let Some(key) = &f.key {
            by_key.entry(key).or_default().push(f);
        }
    }
    let known: BTreeSet<&str> = tracked
        .iter()
        .flat_map(|t| t.keys.iter().map(String::as_str))
        .collect();
    let verdicts = tracked
        .iter()
        .map(|t| {
            let mine: Vec<&Filed> = t
                .keys
                .iter()
                .filter_map(|k| by_key.get(k.as_str()))
                .flatten()
                .copied()
                .collect();
            let urls = |state| -> Vec<String> {
                mine.iter()
                    .filter(|f| f.state == state)
                    .map(|f| f.url.clone())
                    .collect()
            };
            let verdict = if t.resolved {
                Verdict::AlreadyResolved
            } else if mine.is_empty() {
                Verdict::NeverFiled
            } else if mine.iter().any(|f| f.state == State::Open) {
                Verdict::Open {
                    open: urls(State::Open),
                }
            } else {
                Verdict::Resolve {
                    proof: urls(State::Closed),
                }
            };
            (t, verdict)
        })
        .collect();
    let unmatched = filed
        .iter()
        .filter(|f| f.key.as_deref().is_none_or(|k| !known.contains(k)))
        .collect();
    Plan {
        verdicts,
        unmatched,
    }
}

pub fn comment(proof: &[String]) -> String {
    let mut text = String::from(
        "Resolved at the almanac cutover: every almanac issue for this fingerprint is closed on GitHub.\n",
    );
    for url in proof {
        text.push_str(&format!("\n- {url}"));
    }
    text
}

/// Prints the plan. Never prints a Bugsink URL, which names the tailnet host.
pub fn report(plan: &Plan<'_>) -> String {
    let (mut resolve, mut open, mut never, mut resolved) = (vec![], vec![], vec![], vec![]);
    for (t, verdict) in &plan.verdicts {
        let label = format!("{} [{}]", t.keys.join(" | "), t.name);
        match verdict {
            Verdict::Resolve { proof } => resolve.push(format!(
                "{label}\n    proof: {}",
                proof.join("\n           ")
            )),
            Verdict::Open { open: urls } => {
                open.push(format!("{label}\n    open: {}", urls.join("\n          ")))
            }
            Verdict::NeverFiled => never.push(label),
            Verdict::AlreadyResolved => resolved.push(label),
        }
    }
    let unmatched = plan.unmatched.iter().map(|f| {
        let state = match f.state {
            State::Open => "open",
            State::Closed => "closed",
        };
        let key = f.key.as_deref().unwrap_or("no service/tool/class in body");
        format!("{} ({state}): {key}", f.url)
    });
    let sections = [
        ("resolve", resolve),
        ("left open: an almanac issue is still open", open),
        (
            "left open: never filed (below the filer's threshold)",
            never,
        ),
        ("already resolved", resolved),
        (
            "almanac issues that match no Bugsink issue",
            unmatched.collect(),
        ),
    ];
    let mut out = String::new();
    for (title, lines) in sections {
        out.push_str(&format!("\n{title} ({}):\n", lines.len()));
        for line in lines {
            out.push_str(&format!("  {line}\n"));
        }
    }
    out
}

fn http() -> Client {
    Client::builder()
        .timeout(TIMEOUT)
        .build()
        .expect("an HTTP client with a timeout builds")
}

/// Sends and reads JSON. The error never carries the URL, which may name the tailnet host.
async fn send(request: RequestBuilder, what: &str) -> Result<Value, String> {
    let response = request
        .send()
        .await
        .map_err(|e| format!("{what}: {}", e.without_url()))?;
    let status = response.status();
    if !status.is_success() {
        return Err(format!("{what}: answered {status}"));
    }
    response
        .json()
        .await
        .map_err(|e| format!("{what}: {}", e.without_url()))
}

pub struct Github {
    http: Client,
    token: String,
}

#[derive(Deserialize)]
struct GithubIssue {
    html_url: String,
    state: String,
    body: Option<String>,
    pull_request: Option<Value>,
}

impl Github {
    pub fn new(token: String) -> Github {
        Github {
            http: http(),
            token,
        }
    }

    /// Every `almanac`-labelled issue in `repo`, open or closed.
    pub async fn filed(&self, repo: &str) -> Result<Vec<Filed>, String> {
        let mut out = Vec::new();
        for page in 1.. {
            let request = self
                .http
                .get(format!(
                    "https://api.github.com/repos/{repo}/issues?labels=almanac&state=all&per_page=100&page={page}"
                ))
                .bearer_auth(&self.token)
                .header("Accept", "application/vnd.github+json")
                .header("User-Agent", "brined-almanac");
            let issues: Vec<GithubIssue> =
                serde_json::from_value(send(request, &format!("GitHub {repo} issues")).await?)
                    .map_err(|e| format!("GitHub {repo} issues: {e}"))?;
            let last = issues.len() < 100;
            out.extend(
                issues
                    .into_iter()
                    .filter(|i| i.pull_request.is_none())
                    .map(|i| Filed {
                        key: i.body.as_deref().and_then(key_in_body),
                        state: if i.state == "open" {
                            State::Open
                        } else {
                            State::Closed
                        },
                        url: i.html_url,
                    }),
            );
            if last {
                break;
            }
        }
        Ok(out)
    }
}

pub struct Bugsink {
    http: Client,
    api: String,
    project: String,
    token: String,
}

#[derive(Deserialize)]
struct Page<T> {
    next: Option<String>,
    results: Vec<T>,
}

#[derive(Deserialize)]
struct BugsinkIssue {
    id: String,
    friendly_id: String,
    is_resolved: bool,
    first_seen: String,
}

#[derive(Deserialize)]
struct Grouping {
    grouping_key: String,
}

impl Bugsink {
    pub fn new(dsn: &Dsn, token: String) -> Bugsink {
        Bugsink {
            http: http(),
            api: format!("{}/api/canonical/0", dsn.base_url()),
            project: dsn.project().to_string(),
            token,
        }
    }

    async fn all<T: for<'de> Deserialize<'de>>(
        &self,
        first: String,
        what: &str,
    ) -> Result<Vec<T>, String> {
        let mut out = Vec::new();
        let mut next = Some(first);
        while let Some(url) = next {
            let request = self.http.get(url).bearer_auth(&self.token);
            let page: Page<T> = serde_json::from_value(send(request, what).await?)
                .map_err(|e| format!("{what}: {e}"))?;
            out.extend(page.results);
            next = page.next;
        }
        Ok(out)
    }

    /// Every issue in the project, resolved or not, with its grouping keys.
    pub async fn tracked(&self) -> Result<Vec<Tracked>, String> {
        let issues: Vec<BugsinkIssue> = self
            .all(
                format!("{}/issues/?project={}&state=all", self.api, self.project),
                "Bugsink issue list",
            )
            .await?;
        let mut out = Vec::with_capacity(issues.len());
        for i in issues {
            let keys: Vec<Grouping> = self
                .all(
                    format!("{}/issues/{}/grouping/", self.api, i.id),
                    &format!("Bugsink grouping of {}", i.friendly_id),
                )
                .await?;
            out.push(Tracked {
                id: i.id,
                name: i.friendly_id,
                keys: keys.into_iter().map(|g| g.grouping_key).collect(),
                resolved: i.is_resolved,
                first_seen: i.first_seen,
            });
        }
        Ok(out)
    }

    /// Comments first, so a failure before the resolve leaves the proof on an unresolved issue
    /// that the next `--apply` picks up again.
    pub async fn resolve(&self, issue: &Tracked, proof: &[String]) -> Result<(), String> {
        let commented = self
            .http
            .post(format!("{}/issue-comments/", self.api))
            .bearer_auth(&self.token)
            .json(&json!({"issue": issue.id, "comment": comment(proof)}));
        send(commented, &format!("comment on {}", issue.name)).await?;
        let resolved = self
            .http
            .post(format!("{}/issues/{}/resolve/", self.api, issue.id))
            .bearer_auth(&self.token);
        send(resolved, &format!("resolve {}", issue.name)).await?;
        Ok(())
    }
}

/// Reads both sides, prints the plan, and with `apply` resolves and comments.
pub async fn run(lookup: impl Fn(&str) -> Option<String>, apply: bool) -> Result<(), String> {
    let need = |name: &str| {
        lookup(name)
            .filter(|v| !v.is_empty())
            .ok_or_else(|| format!("{name} is not set"))
    };
    let dsn: Dsn = need("BUGSINK_DSN")?.parse()?;
    let bugsink = Bugsink::new(&dsn, need("BUGSINK_API_TOKEN")?);
    let github = Github::new(need("GITHUB_TOKEN")?);

    let mut filed = Vec::new();
    for repo in REPOS {
        filed.extend(github.filed(repo).await?);
    }
    let tracked = bugsink.tracked().await?;
    let plan = plan(&filed, &tracked);

    let count = |state| filed.iter().filter(|f| f.state == state).count();
    let seen: BTreeSet<&str> = tracked.iter().map(|t| t.first_seen.as_str()).collect();
    println!(
        "cutover: {} Bugsink issue(s), first seen {} to {}; {} almanac issue(s), {} open and {} closed",
        tracked.len(),
        seen.first().unwrap_or(&"-"),
        seen.last().unwrap_or(&"-"),
        filed.len(),
        count(State::Open),
        count(State::Closed),
    );
    print!("{}", report(&plan));

    let due: Vec<(&Tracked, &Vec<String>)> = plan
        .verdicts
        .iter()
        .filter_map(|(t, v)| match v {
            Verdict::Resolve { proof } => Some((*t, proof)),
            _ => None,
        })
        .collect();
    if !apply {
        println!(
            "\ncutover: dry run, would resolve {} issue(s); pass --apply to resolve and comment",
            due.len()
        );
        return Ok(());
    }
    for (i, (issue, proof)) in due.iter().enumerate() {
        bugsink
            .resolve(issue, proof)
            .await
            .map_err(|e| format!("resolved {i} of {}, then: {e}", due.len()))?;
        println!("cutover: resolved {}", issue.name);
    }
    println!("\ncutover: resolved {} issue(s)", due.len());
    Ok(())
}

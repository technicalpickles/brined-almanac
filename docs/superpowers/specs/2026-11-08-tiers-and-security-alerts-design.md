# Tiers in code, and security-alerts

**Status:** design, 2026-11-08. The next slice of roadmap M1, after the
taskwarrior tracker (`2026-11-08-taskwarrior-tracker-design.md`). It covers two
M1 bullets: tier declarations in code, and security-alerts, the first workflow
that files `task:<project>` targets.

## Goal

An open security alert on a repository I care about turns into planned work
when nothing else is going to fix it, and a rescan files nothing new. Which
repositories I care about comes from their declared tier, so the tiers move
from `docs/tiers.md` into code first.

security-alerts is a backstop, not the fixer. Renovate and Dependabot open fix
pull requests, and upstream's `almanacs/renovate` vets and repairs Renovate's.

Each alert gets a window, a number of days that depends on its severity: 14
days for a high, for example. GitHub closes an alert when the fix for it
merges. So an alert that is still open when its window ends is one the bots
did not fix in time, either because no fix pull request was opened or because
one was opened and is stuck. Only then does this workflow file a task. An alert
that gets fixed inside its window never reaches taskwarrior.

That is the rule upstream's roadmap M2 "Exposure" names as "filing work when no
fix appears within a severity's window", so the workflow can move upstream
later and this copy can go.

Non-goals:

- Vetting or repairing fix pull requests. Upstream's `security_fixes` and
  `build_failures` do that for Renovate. Teaching them Dependabot is an upstream
  proposal (see "Upstream").
- Spotting whether an open pull request already fixes an alert. The window
  covers it: a fix that merges in time closes the alert, and a fix still open
  past the window is stuck and worth a task anyway.
- Code scanning. Nothing runs it on any watched repository.
- Turning on alerts and fix pull requests. That is configuration, and it is
  the follow-up that runs next to this work (see "Follow-up: get fix pull
  requests flowing").
- The archive question for inactive repositories. No watched repository is
  inactive today.
- Filing issues. GitHub already has a record for each part of this. The alert
  is GitHub's record of the vulnerability, and a Renovate or Dependabot pull
  request is the record of its fix. Renovate's Dependency Dashboard issue lists
  pending updates too, on repositories that turn it on. An issue from almanac
  would repeat all three. What none of them do is put the work on my plan when
  no fix arrives, so that case is a task (see "Where findings go").

## What the data says

Measured with `gh api` on 2026-11-08 across the public repositories in
`docs/tiers.md`:

| Repository | Open Dependabot alerts | Days the open set changed, last 90 days |
|---|---|---|
| sb | 63 (2 critical, 22 high, 34 medium, 5 low) | 2 |
| dotfiles | 18 (12 high, 5 medium, 1 low) | 4 |
| cq | 14 (1 critical, 4 high, 4 medium, 5 low) | 4 |
| pickled-claude-plugins | 5 (4 medium, 1 low) | 1 |
| picklehome, bktide | 0 | 0 |

- All 100 alerts are in lockfiles, and 99 name a patched version.
- Most are transitive. All 18 on dotfiles are, and 54 of sb's 63 are. cq's 14
  are in `Cargo.lock`, which the API does not classify.
- No watched repository has an open Renovate or Dependabot pull request. dotfiles,
  cq, and bktide have a `renovate.json`. Renovate has opened one pull request on
  dotfiles and none on cq or bktide. No repository has a `dependabot.yml`.
- Dependabot alerts are off on brined-almanac and on every private repository
  checked. Secret scanning is off everywhere except brined-almanac, which has no
  alerts.

Two things follow. A daily item per repository, as `docs/workflows.md` says
today, would have filed up to 360 tasks in those 90 days, one a day for each
of the four repositories with alerts. One task per alert
would file 100 on the first scan. A task per change of the overdue set files a
few a month. And the biggest single lever is configuration, not code: fix pull
requests are not being opened at all.

## Tiers in code

### Shape

```rust
#[derive(Deserialize, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
#[serde(rename_all = "lowercase")]
pub enum Tier { Dormant, Deployed, Active, Maintained }

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Declaration {
    name: Slug,              // owner/repo
    tiers: BTreeSet<Tier>,   // not empty
    signal: Option<String>,  // what says it is in use; documentation only
    note: Option<String>,    // documentation only
}

pub struct Tiers(BTreeMap<Slug, BTreeSet<Tier>>);

impl Tiers {
    pub fn load(local: Option<&Path>) -> Result<Tiers, String>;
    pub fn watched(&self, any_of: &[Tier]) -> Vec<&Slug>;
}
```

The enum is the only place a tier is defined. A file that names any other tier
fails to parse, which is the rule in `docs/workflows.md` that a file "can only
add a repository to a tier that code defines". Handed off and retired stay
prose in `docs/tiers.md`: nothing watches them, and their repositories are not
cloned here.

`deny_unknown_fields` makes a typo such as `teirs` an error instead of a
repository that silently has no tiers.

### Two files, one schema

- **`tiers.toml`**, committed at the repository root, holds every repository
  `docs/tiers.md` declares today. `include_str!` compiles it in, so the binary
  never runs without it.
- **`local/tiers.toml`**, gitignored, holds the private repositories nothing
  public names. It already exists with this exact schema. The daemon reads it
  from the path in `LOCAL_TIERS`.

`Tiers::load` parses both and merges them. A repository declared in both is an
error, so each repository has one declaration. `LOCAL_TIERS` unset loads the
public file alone and says so on stderr, which suits a dev checkout. `LOCAL_TIERS`
set to a path that is missing or does not parse is a startup error, because a
silent fallback would drop ten repositories from every scan.

`docs/tiers.md` loses its tables. Each table row becomes a `[[repo]]` entry,
with the usage signal and note carried in `signal` and `note`. The doc keeps the
explanation, the handed-off and retired prose, and the unclear list, and points
at `tiers.toml` as the declaration. Commit counts from the 2026-11-08 survey
stay in the doc as a dated snapshot, not in the file.

### Prod

`scripts/deploy` fetches the vault copy with
`op document get tiers.toml --vault brined-almanac` into
`${BRINED_ALMANAC_STATE}/tiers.toml`, mode 0600. It mounts that file read-only
into the daemon at `/etc/brined-almanac/tiers.toml` and sets `LOCAL_TIERS` to
the mounted path. If the fetch fails, the deploy stops. The service account
already reads the vault. The header of `local/tiers.toml` has the commands that
create and update the vault document.

`providers::all` provides `Tiers` the way it provides `Transcripts`.

## security-alerts

### Which repositories

Every repository whose tiers include deployed, active, or maintained. Dormant
repositories are not scanned.

### What a scan reads

Upstream's `GithubClient` reports any 4xx as `ForgeRefused { status }` and
drops the body, so the scan tells "turned off" apart by status alone. A
disabled Dependabot answers its alerts endpoint with 403, the same status as a
token that lacks a permission, so the scan asks a different endpoint first.
For each repository, with a read-only client, in this order:

1. `GET /repos/{repo}`. A 404 fails the scan with a message that names the
   repository and says to check the tiers files. The `private` flag feeds the
   coverage rule.
2. `GET /repos/{repo}/vulnerability-alerts`, through `get_text`, since the
   success answer is an empty 204. 204 means Dependabot alerts are on, and 404
   means they are off. Checked 2026-11-08: dotfiles answers 204, brineworks
   404.
3. When step 2 said on: `GET /repos/{repo}/dependabot/alerts?state=open`
   through `get_all`.
4. `GET /repos/{repo}/secret-scanning/alerts?state=open` through `get_all`.
   A 404 means secret scanning is off, since step 1 proved the repository
   exists. The response includes the secret itself in `secret`. The parser
   never reads that field, and a test pins it out of every body.

Each feature parses at the boundary into:

```rust
enum Coverage<A> { On(Vec<A>), Off }
```

Any other error fails the scan with the repository and status in the message,
so a bad token or a rate limit is loud.

### The overdue rule

```rust
#[derive(Clone, Copy)]
pub enum Severity { Low, Medium, High, Critical, Unknown }

pub enum Alert {
    Dependency { number: u64, severity: Severity, package: String,
                 manifest: String, ghsa: String, patched: Option<String>,
                 created: Date, url: String },
    Secret { number: u64, kind: String, created: Date, url: String },
}

/// The window an alert gets before it is overdue.
pub const WINDOWS: &[(Kind, Days)] = &[
    (Kind::Secret, 0),
    (Kind::Dependency(Severity::Critical), 2),
    (Kind::Dependency(Severity::High), 14),
    (Kind::Dependency(Severity::Medium), 30),
    (Kind::Dependency(Severity::Unknown), 30),
    (Kind::Dependency(Severity::Low), 90),
];

pub fn overdue(alerts: &[Alert], today: Date) -> Vec<&Alert>;
```

An alert is overdue when `today - created` is at least its window. The windows
live in one table next to the rule. They are starting numbers to tune from what
the tasks show, the same way the roadmap treats the budget rule's numbers. A
leaked secret has no window: rotating it is never a pull request's job.

`Severity` copies the names of upstream's `advisory::Severity`. The workflow
does not depend on the `advisory` crate for one enum. If upstream M2 lands, the
workflow moves there and uses theirs.

### Events and keys

The scan emits at most one event per repository, and at most one coverage
event per scan:

- **`SecurityOverdue`**, for a repository with one or more overdue alerts. It
  carries the repository, its taskwarrior project, counts by severity, and the
  overdue alerts. Its delivery ID is
  `scan:security-alerts/<owner>/<repo>@<digest>`. The digest is the first 12
  hex characters of SHA-256 over the sorted list of `dependabot:<number>` and
  `secret:<number>` for the overdue alerts. The store dedupes by delivery ID,
  so the same overdue set files once. A new alert crossing its window, or an
  overdue alert getting fixed, changes the digest and files a fresh task that
  lists everything overdue now.
- **`CoverageGaps`**, listing every scanned repository where Dependabot alerts
  are `Off`, and every public scanned repository where secret scanning is
  `Off`. Secret scanning on a private repository needs a paid GitHub plan, so a
  private repository with it off is not a gap. The delivery ID is
  `scan:security-alerts/coverage@<digest>` over the sorted gap list, so the task
  refiles only when the set of gaps changes.

The digest needs a hash that is stable across builds, so `sha2` joins the
dependencies. `std`'s `DefaultHasher` is not stable across Rust versions.

An older task for the same repository stays open when its overdue set changes.
Its body lists its alert numbers. M2's task-reconciler can close it on checked
evidence: every listed alert fixed or dismissed in GitHub, or a newer
security-alerts task for the same repository. Until then I close it by hand.
On today's numbers that is a few tasks a month.

### Where findings go

Every finding here is a task. `SecurityOverdue` files to
`task:<repo name>`, built with `Project::from_str` from the repository's name
without its owner. `CoverageGaps` files to `task:brined-almanac`, because the
gap is in what almanac can see.

This departs from the line in `docs/workflows.md` that sends a fix in a private
repository to an issue there. No private watched repository has alerts turned
on, so the branch would have no data to route. If one does later, the router
already handles an issue target, and the change is one target expression.

### The workflow

The scan does the reading and the deciding that depends on time. The workflow
formats, and the policy files:

- `facts(event)` builds the title from clean fields only:
  `security-alerts: 18 overdue on dotfiles (12 high, 5 medium, 1 low)`. The
  body is `Untrusted`, because package names and manifest paths come from
  lockfiles. It lists one line per alert, grouped by severity: number,
  package, manifest, GHSA ID, patched version, age in days, and URL. A secret
  line names its type, age, and URL.
- `policy(facts)` returns one `Effect::File` with the target above.
- `may = [File]`, and `runs_per_hour` stays well above one scan's worth of
  repositories.

The 15-minute scheduled scan means an alert files within 15 minutes of
becoming overdue.

### Token

A new fine-grained personal access token, item `github-alerts-token` in the
`brined-almanac` vault:

- Repositories: all repositories.
- Dependabot alerts: read.
- Secret scanning alerts: read.
- Administration: read. GitHub's docs list it for the vulnerability-alerts
  check in step 2. Confirm when creating the token, by running step 2 with it.
- Metadata: read.

It reaches the daemon as `GITHUB_ALERTS_TOKEN`, through
`BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=op://brined-almanac/github-alerts-token/token`
in `prod.secrets.env`. The issues token keeps its narrow scope. All
repositories, rather than a selected list, means adding a repository to a
tiers file never also needs a token edit. The cost is that the token can read
alert lists on repositories nothing watches.

With no `GITHUB_ALERTS_TOKEN`, the scan prints a skip message to stderr and
finds nothing, so other workflows' scans still run. Production cannot reach
this, because the compose file requires the token, and a bad token still fails
the scan with a 401. The dry run does not need it (see "Testing").

## Doc changes

- `docs/workflows.md`, security-alerts: replace "one item per repository per
  day" with the overdue rule, the windows, and the coverage task. Drop the fix
  pull request clause and the issue routing, and point at this spec.
- `docs/vision.md`, "What success looks like": "An open security alert ...
  becomes tracked work within a day" becomes "... has a fix merged, or a task,
  by the end of its severity's window."
- `docs/roadmap.md`, M1: the tiers bullet and the security-alerts bullet link
  here. The done-when changes from "every open security alert on those
  repositories is tracked as one task or issue per repository" to "every
  overdue security alert on those repositories is on a task for its
  repository".
- `docs/tiers.md`: as in "Two files, one schema".
- `docs/runbooks/prod-on-the-mac.md`: the new token item, the tiers document
  fetch, and the mount.

## Testing

Unit tests, all on behavior a caller sees:

- `tiers.toml` parses, and every entry has at least one tier.
- A local file that repeats a public repository, names an unknown tier, or has
  an unknown key fails `Tiers::load` with a message naming the repository or
  key.
- `LOCAL_TIERS` set to a missing path is an error. Unset loads the public file
  alone.
- `watched(&[Deployed, Active, Maintained])` leaves out a dormant-only
  repository and keeps one that is dormant and deployed.
- `overdue` at each window boundary: one day before is not overdue, the day of
  is.
- The digest does not change when the alerts arrive in a different order, and
  does change when one alert is added or removed.
- Status mapping: vulnerability-alerts 404 is Dependabot `Off`, secret
  scanning 404 after a found repository is `Off`, a repository 404 fails the
  scan naming the tiers files, and a 401 or 403 anywhere fails the scan.
- A recorded secret-scanning response with a `secret` value produces a body
  that does not contain that value.

Dry run, with no token and no database: `brined-almanac simulate` on a
recorded `SecurityOverdue` payload under `tests/fixtures/security_alerts/`
prints the task it would file through `LogTracker`.

Live, before merging the second pull request, with the new token:

1. `brined-almanac backfill --dry-run` lists the repositories and overdue
   counts. Compare them with `gh api` counts for the same repositories, using
   the windows above.
2. In a dev stack with Postgres, run `backfill` twice against a local
   taskchampion server. The first run files one task per repository with
   overdue alerts, plus one coverage task. The second files nothing.

## Delivery

Two pull requests, in order:

1. **Tiers.** `src/tiers.rs`, `tiers.toml`, the `docs/tiers.md` cut, the
   `LOCAL_TIERS` provider, and the deploy fetch and mount. Nothing reads `Tiers`
   yet except a startup check that loads it, so a bad prod file fails the
   deploy before security-alerts depends on it.
2. **security-alerts.** `src/workflows/security_alerts.rs`, the alerts client
   provider, the token docs, and the doc changes above.

Steps I do by hand: create the `github-alerts-token` item, and check that the
`tiers.toml` document exists in the vault and matches `local/tiers.toml`.

## Follow-up: get fix pull requests flowing

security-alerts only catches what the bots miss, and today the bots open
nothing. This follow-up runs next to the two pull requests above and is
taskwarrior task `f2ed310f-f842-44ff-997f-ed023bd6665e`:

- Turn on Dependabot alerts on every watched repository. They are free on
  private repositories, and Renovate's `vulnerabilityAlerts` reads them, so
  both bots need them. Alerts alone open no pull requests: Dependabot security
  updates are a separate setting, and it is off on dotfiles
  (`security_and_analysis.dependabot_security_updates`, checked 2026-11-08).
- Pick the bot per repository. Renovate already has a config on dotfiles, cq,
  and bktide, and upstream's `security_fixes` vets its pull requests. Dependabot
  security updates fix transitive lockfile dependencies, which make up most of
  the open alerts. Check on dotfiles whether Renovate opens fixes for its 18
  transitive alerts once `vulnerabilityAlerts` is set up, before choosing.
- Check that the Renovate app is installed where a `renovate.json` exists. cq
  and bktide have one and have never had a Renovate pull request.

The coverage task from security-alerts lists the repositories where alerts are
still off, so it doubles as this follow-up's checklist.

## Upstream

- **Dependabot in `almanacs/renovate`.** `security_fixes` vets Renovate
  security pull requests only. It finds them by Renovate's login, a `renovate/`
  branch, and a parser for Renovate's pull request body (`update_of`). Dependabot
  security updates fix transitive lockfile dependencies, which make up most of
  the alerts above. Teaching the workflow a second bot needs a login, a branch
  prefix, and a body parser per bot. The checks stay as they are. Filed as
  [ivy/almanac#23](https://github.com/ivy/almanac/issues/23) on 2026-11-08,
  tracked by task `1d43272f-51e4-4f09-9bdf-93df4d0acb72`.
- **The overdue rule.** It is upstream M2's "filing work when no fix appears
  within a severity's window". When upstream builds M2, offer this workflow's
  rule and windows, and delete this copy once upstream's covers the same
  repositories.

## Open questions

- The windows are guesses. 14 days for high is long enough for a weekly Renovate
  schedule and upstream's 72-hour cooldown, and short enough that a high alert
  does not sit for a month. The first month of tasks will say whether they
  need moving.
- Whether the coverage task should name secret scanning on public
  repositories, or Dependabot alerts only. Today it would list six public
  repositories with secret scanning off.

//! Decides what a closed almanac issue means for its Bugsink issue, so that closing one never
//! silences its failure. Plan: `docs/superpowers/plans/2026-11-10-resolve-on-close.md`.

use time::OffsetDateTime;

use crate::cutover::{Linked, Reason, Standing};

/// Events in one day that wake an issue muted because its almanac issue closed as not planned.
/// A default to tune: a wrong not-planned close would otherwise be silent forever.
pub const MUTE_WAKE_EVENTS: u64 = 50;

/// An unresolved, unmuted Bugsink issue and what its history says.
#[derive(Clone, Debug)]
pub struct Open {
    pub id: String,
    /// How many times it came back after being resolved, as `bugsink_api::generation` counts.
    pub generation: u64,
    pub unmuted_at: Option<OffsetDateTime>,
}

#[derive(Debug, PartialEq, Eq)]
pub enum Verdict {
    Resolve { proof: String },
    Mute { proof: String },
    Leave,
}

/// Acts only when the newest almanac issue for the Bugsink issue is closed, was filed for its
/// current generation, and says why it closed.
///
/// Newest wins because an older closed issue says nothing about a failure that came back.
/// The generation must match because a regression stays open in Bugsink until its new almanac
/// issue files, and the old closed issue still carries the same link: without the check, the
/// scan would resolve the regression again before it was ever reported.
///
/// A not-planned close is skipped when the issue was unmuted since: its volume condition fired
/// or a person woke it, and muting it again would swallow the wake.
pub fn decide(open: &Open, linked: &[Linked]) -> Verdict {
    let Some(newest) = linked
        .iter()
        .filter(|l| l.issue == open.id)
        .max_by_key(|l| (l.created_at, l.number))
    else {
        return Verdict::Leave;
    };
    if newest.generation != Some(open.generation) {
        return Verdict::Leave;
    }
    let Standing::Closed { reason, at } = newest.standing else {
        return Verdict::Leave;
    };
    let proof = newest.url.clone();
    match reason {
        Some(Reason::Completed) => Verdict::Resolve { proof },
        Some(Reason::NotPlanned) if open.unmuted_at.is_none_or(|woke| woke < at) => {
            Verdict::Mute { proof }
        }
        _ => Verdict::Leave,
    }
}

pub fn resolve_comment(proof: &str) -> String {
    format!("Resolved by the almanac scan: its almanac issue was closed as completed.\n\n- {proof}")
}

pub fn mute_comment(proof: &str) -> String {
    format!(
        "Muted by the almanac scan: its almanac issue was closed as not planned. It wakes at {MUTE_WAKE_EVENTS} events in a day.\n\n- {proof}"
    )
}

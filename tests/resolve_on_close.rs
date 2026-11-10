use brined_almanac::cutover::{Linked, Reason, Standing};
use brined_almanac::resolve_on_close::{Open, Verdict, decide};
use time::OffsetDateTime;
use time::macros::datetime;

const ID: &str = "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01";
const OTHER: &str = "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e02";

fn open(generation: u64) -> Open {
    Open {
        id: ID.into(),
        generation,
        unmuted_at: None,
    }
}

fn closed(reason: Option<Reason>) -> Standing {
    Standing::Closed {
        reason,
        at: datetime!(2026-11-09 12:00 UTC),
    }
}

fn filed(number: u64, generation: u64, created: OffsetDateTime, standing: Standing) -> Linked {
    Linked {
        url: format!("gh/{number}"),
        number,
        issue: ID.into(),
        generation: Some(generation),
        created_at: created,
        standing,
    }
}

fn resolve(number: u64) -> Verdict {
    Verdict::Resolve {
        proof: format!("gh/{number}"),
    }
}

#[test]
fn closed_as_completed_resolves_and_cites_the_close() {
    let gh = [filed(
        5,
        0,
        datetime!(2026-11-05 09:00 UTC),
        closed(Some(Reason::Completed)),
    )];
    assert_eq!(decide(&open(0), &gh), resolve(5));
}

#[test]
fn closed_as_not_planned_mutes() {
    let gh = [filed(
        5,
        0,
        datetime!(2026-11-05 09:00 UTC),
        closed(Some(Reason::NotPlanned)),
    )];
    assert_eq!(
        decide(&open(0), &gh),
        Verdict::Mute {
            proof: "gh/5".into()
        }
    );
}

#[test]
fn a_close_that_says_nothing_about_why_is_left_alone() {
    let gh = [filed(5, 0, datetime!(2026-11-05 09:00 UTC), closed(None))];
    assert_eq!(decide(&open(0), &gh), Verdict::Leave);
}

#[test]
fn the_newest_almanac_issue_decides_whatever_the_older_ones_say() {
    let old_closed = filed(
        5,
        0,
        datetime!(2026-11-05 09:00 UTC),
        closed(Some(Reason::Completed)),
    );
    let new_open = filed(6, 0, datetime!(2026-11-07 09:00 UTC), Standing::Open);
    assert_eq!(
        decide(&open(0), &[old_closed.clone(), new_open.clone()]),
        Verdict::Leave
    );
    assert_eq!(
        decide(&open(0), &[new_open, old_closed]),
        Verdict::Leave,
        "the order of the list does not matter"
    );

    let old_open = filed(5, 0, datetime!(2026-11-05 09:00 UTC), Standing::Open);
    let new_closed = filed(
        6,
        0,
        datetime!(2026-11-07 09:00 UTC),
        closed(Some(Reason::Completed)),
    );
    assert_eq!(decide(&open(0), &[old_open, new_closed]), resolve(6));
}

#[test]
fn issues_filed_in_the_same_second_are_ordered_by_number() {
    let at = datetime!(2026-11-05 09:00 UTC);
    let a = filed(5, 0, at, closed(Some(Reason::Completed)));
    let b = filed(6, 0, at, Standing::Open);
    assert_eq!(decide(&open(0), &[a, b]), Verdict::Leave);
}

#[test]
fn a_regression_is_not_resolved_again_before_its_new_issue_files() {
    let old = filed(
        5,
        0,
        datetime!(2026-11-05 09:00 UTC),
        closed(Some(Reason::Completed)),
    );
    assert_eq!(
        decide(&open(1), std::slice::from_ref(&old)),
        Verdict::Leave,
        "Bugsink is at generation 1, the closed issue was filed at 0"
    );

    let regression = filed(6, 1, datetime!(2026-11-09 09:00 UTC), Standing::Open);
    assert_eq!(decide(&open(1), &[old, regression]), Verdict::Leave);
}

#[test]
fn a_body_whose_generation_could_not_be_read_never_matches() {
    let mut unreadable = filed(
        5,
        0,
        datetime!(2026-11-05 09:00 UTC),
        closed(Some(Reason::Completed)),
    );
    unreadable.generation = None;
    assert_eq!(decide(&open(0), &[unreadable]), Verdict::Leave);
}

#[test]
fn a_bugsink_issue_with_no_almanac_issue_is_left_alone() {
    assert_eq!(decide(&open(0), &[]), Verdict::Leave);
    let mut elsewhere = filed(
        5,
        0,
        datetime!(2026-11-05 09:00 UTC),
        closed(Some(Reason::Completed)),
    );
    elsewhere.issue = OTHER.into();
    assert_eq!(decide(&open(0), &[elsewhere]), Verdict::Leave);
}

#[test]
fn a_muted_issue_that_woke_after_the_close_is_not_muted_again() {
    let gh = [filed(
        5,
        0,
        datetime!(2026-11-05 09:00 UTC),
        closed(Some(Reason::NotPlanned)),
    )];
    let woke = |at| Open {
        unmuted_at: Some(at),
        ..open(0)
    };
    assert_eq!(
        decide(&woke(datetime!(2026-11-09 13:00 UTC)), &gh),
        Verdict::Leave
    );
    assert_eq!(
        decide(&woke(datetime!(2026-11-09 11:00 UTC)), &gh),
        Verdict::Mute {
            proof: "gh/5".into()
        },
        "an unmute before the close is an older decision"
    );
}

#[test]
fn a_wake_does_not_hold_back_a_completed_close() {
    let gh = [filed(
        5,
        0,
        datetime!(2026-11-05 09:00 UTC),
        closed(Some(Reason::Completed)),
    )];
    let woke = Open {
        unmuted_at: Some(datetime!(2026-11-09 13:00 UTC)),
        ..open(0)
    };
    assert_eq!(decide(&woke, &gh), resolve(5));
}

# Don't start agents while the operator is using their Claude window

I run my almanac on the machine I work on, against the same Claude subscription I use all day, so its agents draw on the same five-hour usage window as my own interactive Claude Code sessions. `token_budget`, `max_turns`, and `max_budget_usd` keep one agent session bounded, but nothing stops almanac from spending the window while I'm actively using it. On a busy day I hit the limit without help.

The rule I'm planning:

- No agent session starts during waking hours if one of my interactive sessions wrote to its transcript in the last 30 minutes, and none at any hour if one wrote in the last 5.
- Except when capacity is about to expire: more than half the window unused, with less than 90 minutes to reset.

The inputs are observable. The harness already runs `claude -p --verbose --output-format stream-json`, and that stream carries a `rate_limit_event` whose `unifiedWindows.five_hour` has `utilization` and `resetsAt`. Interactive transcripts land under `~/.claude/projects/`. Those reads would need recording through `Cx` to satisfy principle 7.

Concretely, I'm picturing a gate the almanac supplies, checked where the run budget is checked, before any agent starts. Not sure how many people run almanac next to their own Claude use, so I'm curious whether this belongs in the library or stays in my almanac.

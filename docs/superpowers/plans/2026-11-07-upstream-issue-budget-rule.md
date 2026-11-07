# Don't spend the operator's usage window while they're using it

I run almanac on the same Claude subscription I use all day, and agents draw on the same five-hour window my own sessions do. The per-session caps on tokens, turns, and dollars keep one run bounded, but nothing stops almanac from spending the window while I'm in the middle of a burst. On a busy day I already hit the limit without help.

The rule I'm planning for my almanac:

- No agent run during waking hours while I'm active (a session wrote to its transcript in the last 30 minutes), and none at any hour while any session is working (a write in the last 5 minutes).
- The exception is capacity about to expire: more than half the window unused, with less than 90 minutes to reset.

All of it is observable. A headless `claude -p --output-format stream-json --verbose` run emits a `rate_limit_event` whose `unifiedWindows.five_hour` has `utilization` and `resetsAt`, and transcript writes land under `~/.claude/projects/`.

Not sure how many people run almanac on a personal subscription, so I'm curious whether this belongs next to the existing caps or stays in my almanac.

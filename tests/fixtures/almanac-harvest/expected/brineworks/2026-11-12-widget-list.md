---
date: 2026-11-12
repo: brineworks
issues: [201]
fingerprint:
  service: brineworks
  tool: widget_list
  class: arg-validation
true_class: model-error
verdict: real-open
cause: model-double-nested-args
fix_layer: description
fix_note: "the only example in the skill nests its params"
model: example-model-1
events: 5
real_window: {first: 2026-11-07, last: 2026-11-09}
recurrence: "12 widget_list calls since 2026-11-09, 0 errors"
close:
  201: completed
prs: []
tasks: [11111111-1111-4111-8111-111111111111]
comments:
- https://github.com/technicalpickles/brineworks/issues/201#issuecomment-1000000002
evidence: [transcripts, merged-prs]
skill_gaps: []
worker:
  how: subagent
  model: example-opus
  tokens: 140283
  minutes: 5
---

The `widget_list` tool rejected calls whose params were wrapped one level too deep. The wrapper unwrapped once and the server saw a kwarg it does not define.

Since the last failure: 12 calls, 0 errors. Rewording the skill line is a follow-up task.

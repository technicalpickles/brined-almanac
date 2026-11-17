---
date: 2026-11-12
repo: brineworks
issues: [202]
fingerprint:
  service: brineworks
  tool: widget_list
  class: arg-validation
true_class: model-error
verdict: real-open
cause: wrong-param-names
fix_layer: description
fix_note: "the only example in the skill nests its params"
model: example-model-1
events: 5
real_window: {first: 2026-11-07, last: 2026-11-09}
recurrence: "12 widget_list calls since 2026-11-09, 0 errors"
close:
  202: completed
prs: []
tasks: [11111111-1111-4111-8111-111111111111]
comments:
- https://github.com/technicalpickles/brineworks/issues/202#issuecomment-1000000004
evidence: [transcripts, merged-prs]
skill_gaps: []
worker:
  how: claude-p
  model: example-model-1
  tokens: 987654
  minutes: 7
---

A second cause in the same tool on the same day, so the filenames must not collide.

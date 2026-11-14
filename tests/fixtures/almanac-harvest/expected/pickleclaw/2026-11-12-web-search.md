---
date: 2026-11-12
repo: pickleclaw
issues:
- 301
fingerprint:
  service: openclaw
  tool: web_search
  class: other
true_class: provider-error
verdict: real-open
cause: provider-quota
fix_layer: config
fix_note: check the provider's quota
model: null
events: 3
real_window:
  first: 2026-11-01
  last: 2026-11-12
recurrence: every call failed
close:
  301: hold until 22222222
prs: []
tasks:
- 22222222-2222-4222-8222-222222222222
comments:
- https://github.com/technicalpickles/pickleclaw/issues/301#issuecomment-1000000001
- https://github.com/technicalpickles/pickleclaw/issues/301#issuecomment-1000000003
evidence:
- transcripts
skill_gaps: []
worker:
  how: claude-p
  model: example-model-1
  tokens: 987654
  minutes: 7
---

Intro line.

Search calls fail with HTTP 429 from the provider. Every call in the window failed.

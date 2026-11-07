# An effect for rerunning a CI job that failed on infrastructure

I'm planning a workflow for my almanac that keeps stale pull requests moving, and one small step keeps coming up: rerunning a CI job that failed because a runner went away or a network fetch timed out, when the change itself is fine. There's no effect for it today, so the only option is to `File` or `Ask` it.

Principle 8 makes this touchy. A rerun changes the evidence the repository decides on, and rerunning a flaky failure until it passes is how red builds get merged. So here's the narrow version I had in mind:

- `Effect::Rerun { repo, sha, job }`. The forge refuses if `sha` is no longer the head or the job was already rerun, which caps it at one rerun per commit (principle 6).
- Code classifies the failure as infrastructure from structured data (the job's conclusion and step outcome), never from log text or a model.
- The run publishes an `Attest` under its own context, like `almanac/rerun` with state `success`, so the repository's rules can see a rerun happened.

Not sure this is useful beyond my setup, so I'm mostly curious whether anyone else building on almanac has hit it. I'm not building it until my stale-PR workflow actually runs into flaky CI.

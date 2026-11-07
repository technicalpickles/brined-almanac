# An effect for rerunning a CI job that failed on infrastructure

I'm writing down what my almanac should do with stale dependency pull requests, and one small step keeps coming up: rerunning a CI job that failed because a runner went away or a network fetch timed out, not because of the change. Today there's no effect for it, so the only option is to file it for a person.

I know principle 8 makes this touchy. A rerun changes the evidence the repository decides on, and rerunning a flaky failure until it passes is how red builds get merged. So here's the narrow version I had in mind:

- Code classifies the failure as infrastructure from structured data (the job's conclusion and step outcome), never from log text or a model.
- One rerun per commit, then it's a person's problem.
- The run publishes an `Attest` saying it reran, so the repository's rules can see it happened.

Not sure this is useful beyond my setup, so I'm mostly curious whether anyone else building on almanac has hit the same thing. I'm not planning to build it until my upkeep workflow actually runs into flaky CI.

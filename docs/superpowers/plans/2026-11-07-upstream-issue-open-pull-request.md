# An effect for opening a pull request from a checked patch

I'm building a workflow that continues work I parked at the end of a session: an agent in the sandbox picks up a handoff, writes the next round of code, and returns a patch. `Push` covers every round after the first, since it commits a checked patch to an existing pull request's branch. The first round has nowhere to go, because there's no effect that creates a branch and opens a pull request.

What I had in mind is close to `Push`:

- `Effect::Propose { repo, base, branch, title, body, patch }`, where `patch` is `Checked` like `Push`'s
- The forge creates `branch` from `base`, commits the patch, and opens the pull request. It refuses if `branch` already exists, so a retry can't stack a second pull request.

Merging stays out, so principle 8 holds: almanac proposes and the repository decides. Renovate opens its own pull requests, so the renovate almanac never needed this, but I'd guess anything that does more than repair will. Curious whether anyone else building on almanac has wanted it.

# An effect for opening a pull request from a checked patch

I'm building a workflow that continues unfinished work from a notes file I leave at the end of a coding session. An agent in the sandbox reads the notes, writes the next round of code, and returns a patch. `Push` covers every round after the first, since it commits a checked patch to an existing pull request's branch. The first round has nowhere to go, because no effect creates a branch and opens a pull request.

What I had in mind is close to `Push`:

- `Effect::Propose { repo, base, branch, title, body, patch }`, with `title: Title` and `body: Untrusted<String>` as in `File`, and `patch: Checked` as in `Push`.
- `base` is a branch name, and the forge refuses unless its tip is the `Sha` the patch was checked against. It creates `branch`, commits the patch, and opens the pull request.
- A retry with the same key is a no-op. If `branch` already exists for any other reason, the forge refuses.

Merging stays out, so principle 8 holds: almanac proposes changes and the repository decides. Renovate opens its own pull requests, so almanac-renovate never needed this, but I'd guess any almanac that does more than repair will. Curious whether anyone else has wanted it.

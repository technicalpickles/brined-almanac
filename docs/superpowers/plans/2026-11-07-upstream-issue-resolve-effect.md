# A Resolve effect for closing tracker work on checked evidence

I'm adding a tracker for taskwarrior (a CLI todo list) to my almanac, along the lines of the tracker provider in your M4, and one workflow wants to close tasks as well as file them. It reconciles my backlog: when a merged pull request or a commit on main says `Closes <task uuid>`, the task is done, and today I have to notice that by hand.

The effect module says "Nothing here merges or closes," so I wanted to ask before building it. Principle 8 refuses an effect added for one workflow's convenience, so here's the principle I'd lean on: a tracker is the operator's planning record, not the repository's, and closing an entry on evidence code has checked is closer to `Attest` than to a merge. A model can veto a close but never approve one, so principle 4 holds too.

What I'm picturing:

- `Effect::Resolve { item, evidence }`, with `EffectKind::Resolve` so guards see it. `item` names the tracker entry, and `evidence` is clean, like the merge commit's `Sha`.
- `Tracker::resolve(key, item, evidence)`, where `key` is the retry key, as in `file`.

If this doesn't fit almanac, I'll carry it in a fork, but other trackers might want it too.

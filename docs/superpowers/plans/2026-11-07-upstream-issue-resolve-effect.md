# A Resolve effect for closing tracker work on checked evidence

I'm adding a taskwarrior tracker to my almanac (filing it the way your M4 plans a tracker provider), and one workflow wants to close tasks as well as file them. It reconciles my backlog: when a merged pull request or a commit on main says `Closes <task uuid>`, the task is done, and today a person has to notice that by hand.

The effect module says "Nothing here merges or closes," so I wanted to ask before I build it. My read is that closing a task in my own tracker is closer to `Attest` than to a merge. A task is my planning record, not the repository's, and the close only happens on evidence code has checked. A model can find evidence or veto a close, but never approve one, so principle 4 holds.

What I'm picturing:

- `Effect::Resolve { key, evidence }`, with `EffectKind::Resolve` so guards see it
- `Tracker::resolve(key, evidence)`, where `key` names the tracker item, the same way `file` takes one

If this doesn't fit almanac, I'll carry it in a fork, but it seemed worth asking whether other trackers would want it.

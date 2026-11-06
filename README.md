# brined-almanac

Almanac workflows, built on [ivy/almanac](https://github.com/ivy/almanac), that watch my services and keep them maintained. They're written in Rust.

## What never goes in this repo

This repo is public from its first push, so a few things stay out of it for good:

- Transcript or log text from the bot, including pasted samples and serialized tainted values.
- Secrets, or anything that resolves one, like a token, a key, or a path to a credential.
- The private MCP server config.
- Telegram ids and bot handles.

Hostnames and repo names are fine, since [picklehome](https://github.com/technicalpickles/picklehome) already names them.

## How the guardrails work

Nobody has to remember the rules, because hooks check them. A pre-commit hook runs gitleaks over the staged diff and `scripts/check-forbidden` over the staged files, and pre-push runs both over the whole repo. The forbidden check reads regex patterns from `.forbidden-patterns`, which are safe to commit, and private literal strings from `local/forbidden-strings`, which is gitignored because a denylist of private strings would publish them. On a fresh clone that file doesn't exist, so the script says the literal check is off until you create it. GitHub push protection is on server-side as a last backstop.

Run `mise install && hk install` to arm the hooks, and `mise run check` to run them by hand.

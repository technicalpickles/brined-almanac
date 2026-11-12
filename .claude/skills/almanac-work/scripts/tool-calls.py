#!/usr/bin/env python3
"""Tabulate Brent's calls to one MCP tool from the transcripts brined-almanac pulls.

    python3 tool-calls.py triage_apply
    python3 tool-calls.py triage_apply --since 2026-11-07
    python3 tool-calls.py triage_apply --errors
    python3 tool-calls.py --list
    python3 tool-calls.py --list --since 2026-11-08

Reads $BRINED_ALMANAC_STATE/transcripts, default ~/.local/share/brined-almanac/transcripts.
Prints one row per call: time, surface, input, outcome, seconds, and for an error the line that says why.
Then a summary: calls, errors, longest call, and errors per class per day.
Output may contain transcript text. Read it here, never paste it into a public repository.
"""

import argparse
import collections
import glob
import json
import os
import re
import sys
import tomllib

STATE = os.environ.get("BRINED_ALMANAC_STATE") or os.path.expanduser("~/.local/share/brined-almanac")
TRANSCRIPTS = os.path.join(STATE, "transcripts")
FENCE = re.compile(
    r"<<<EXTERNAL_UNTRUSTED_CONTENT[^>]*>>>\s*Source:[^\n]*\n---\n(.*?)\n?<<<END_EXTERNAL_UNTRUSTED_CONTENT", re.S
)
CLASSES_TOML = os.path.join(os.path.dirname(os.path.abspath(__file__)), "../../../../classes.toml")


def load_classes():
    """The ordered class table the Rust classifier reads too. The last row has no pattern."""
    with open(CLASSES_TOML, "rb") as fh:
        rows = tomllib.load(fh)["class"]
    return [(r["name"], re.compile(r["pattern"]) if "pattern" in r else None) for r in rows]


CLASSES = load_classes()


def surface(path):
    name = os.path.basename(path)
    m = re.match(r"agent-main-([a-z]+)", name)
    return m.group(1) if m else name[:12]


def unwrap(text):
    """The error without OpenClaw's wrapping: the `error` of a JSON object, or its result.content text, then whatever sits
    inside an untrusted-content fence. Its preamble is boilerplate that says Delete and Execute."""
    try:
        wrapped = json.loads(text)
    except ValueError:
        wrapped = None
    if isinstance(wrapped, dict) and isinstance(wrapped.get("error"), str):
        text = wrapped["error"]
    elif isinstance(wrapped, dict) and isinstance(wrapped.get("result"), dict):
        inner = error_text({"details": None, "content": wrapped["result"].get("content")})
        text = inner if inner.strip() else text
    m = FENCE.search(text)
    return m.group(1) if m else text


def error_text(message):
    """What a failed call said, picked as src/transcript.rs error_text does. `details` holds it as
    a string, or as an object with an `error` or a `result`. Any other `details` only names the
    tool and its status, and the message is in the text blocks of `content`."""
    details = message.get("details")
    if isinstance(details, str):
        says_why = bool(details.strip())
    elif isinstance(details, dict):
        says_why = "error" in details or "result" in details
    else:
        says_why = False
    blocks = message.get("content")
    content = "\n".join(
        b["text"] for b in blocks if isinstance(b, dict) and b.get("type") == "text" and isinstance(b.get("text"), str)
    ) if isinstance(blocks, list) else ""
    if not says_why and content.strip():
        return content
    if isinstance(details, str):
        return details
    if details is None:
        return ""
    return json.dumps(details, separators=(",", ":"), sort_keys=True, ensure_ascii=False)


def nested_text(details):
    """The same for an `openclaw.nested-tool.v1` record, which keeps the text under result.content."""
    result = details.get("result")
    if isinstance(result, dict):
        return error_text({"details": None, "content": result.get("content")})
    return error_text({"details": details.get("error"), "content": None})


def error_line(text):
    """The line of the unwrapped text that says why. A `Command failed: <argv>` line hides the
    command's own message on the next line."""
    lines = [ln for ln in unwrap(text).splitlines() if ln.strip()]
    if not lines:
        return ""
    if lines[0].startswith("Command failed:") and len(lines) > 1:
        return lines[1].strip()[:120]
    return lines[0].strip()[:120]


def classify(text):
    text = unwrap(text)
    for name, pattern in CLASSES:
        if pattern is None or pattern.search(text):
            return name


def matches(name, tool):
    return name == tool or name.endswith(f"__{tool}")


def row(ts, path, inp, details, started, text):
    secs = (details.get("timestamp", 0) - started) / 1000 if started and details.get("timestamp") else None
    failed = bool(details.get("isError"))
    return {
        "time": ts[:16],
        "surface": surface(path),
        "input": json.dumps(inp, separators=(",", ":"))[:40] if inp else "",
        "error": bool(details.get("isError")),
        "secs": secs,
        "line": error_line(text) if failed else "",
        "class": classify(text) if failed else "",
    }


def calls(tool, since):
    """Two record shapes carry a tool call's outcome.

    Since about 2026-10-24 each MCP call is an `openclaw.nested-tool.v1` custom message whose
    details hold toolName, input, startedAt and the result. Before that, the outcome is a plain
    `toolResult` message with toolName and isError, and the input lives on the assistant's
    matching `toolCall`, paired by id. Read both, so --since dates before the change are honest.
    """
    rows = []
    for path in sorted(glob.glob(os.path.join(TRANSCRIPTS, "*.jsonl"))):
        pending = {}
        with open(path, errors="replace") as fh:
            for line in fh:
                if tool not in line and "toolCall" not in line:
                    continue
                try:
                    o = json.loads(line)
                except json.JSONDecodeError:
                    continue
                msg = o.get("message", {})
                ts = o.get("timestamp", "")
                for c in msg.get("content", []) if isinstance(msg.get("content"), list) else []:
                    if isinstance(c, dict) and c.get("type") == "toolCall" and matches(c.get("name", ""), tool):
                        pending[c.get("id")] = (c.get("arguments"), msg.get("timestamp") or 0)
                if ts[:10] < since:
                    continue
                if msg.get("customType") == "openclaw.nested-tool.v1":
                    d = msg.get("details", {})
                    if matches(d.get("toolName", ""), tool):
                        rows.append(row(ts, path, d.get("input"), d, d.get("startedAt"), nested_text(d)))
                elif msg.get("role") == "toolResult" and matches(msg.get("toolName", ""), tool):
                    args, started = pending.pop(msg.get("toolCallId"), (None, 0))
                    if isinstance(args, str):
                        try:
                            args = json.loads(args)
                        except json.JSONDecodeError:
                            pass
                    rows.append(row(ts, path, args, msg, started, error_text(msg)))
    rows.sort(key=lambda r: r["time"])
    return rows


def list_tools(since):
    stats = collections.defaultdict(lambda: [0, 0, ""])
    for path in glob.glob(os.path.join(TRANSCRIPTS, "*.jsonl")):
        with open(path, errors="replace") as fh:
            for line in fh:
                if "nested-tool" not in line and '"toolResult"' not in line:
                    continue
                try:
                    o = json.loads(line)
                except json.JSONDecodeError:
                    continue
                msg = o.get("message", {})
                ts = o.get("timestamp", "")
                if ts[:10] < since:
                    continue
                if msg.get("customType") == "openclaw.nested-tool.v1":
                    d = msg.get("details", {})
                    name, failed = d.get("toolName"), d.get("isError")
                elif msg.get("role") == "toolResult":
                    name, failed = msg.get("toolName"), msg.get("isError")
                else:
                    continue
                # the tool_call dispatcher repeats each nested tool's error, so counting it doubles them
                if not name or name == "tool_call":
                    continue
                st = stats[name]
                st[0] += 1
                if failed:
                    st[1] += 1
                    st[2] = max(st[2], ts)
    print(f"{'calls':>6} {'errors':>6}  {'last error':<16}  tool")
    for name, (n, errs, last) in sorted(stats.items(), key=lambda kv: (kv[1][2], kv[1][0]), reverse=True):
        print(f"{n:6d} {errs:6d}  {last[:16] or '-':<16}  {name}")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("tool", nargs="?", help="tool name, with or without the brineworks__ prefix")
    ap.add_argument("--since", default="0000", help="YYYY-MM-DD, keep calls on or after this day")
    ap.add_argument("--errors", action="store_true", help="rows for failed calls only")
    ap.add_argument("--list", action="store_true", help="every tool seen, with calls, errors and last error time, newest error first, and exit")
    args = ap.parse_args()
    if not os.path.isdir(TRANSCRIPTS):
        sys.exit(f"no transcripts at {TRANSCRIPTS}; set BRINED_ALMANAC_STATE or run the prod puller")
    if args.list:
        list_tools(args.since)
        return
    if not args.tool:
        ap.error("tool name required (or --list)")
    tool = args.tool.split("__", 1)[-1]
    rows = calls(tool, args.since)
    print(f"{'time':<16} {'surface':<9} {'input':<40} {'err':<5} {'secs':<7} error")
    for r in rows:
        if args.errors and not r["error"]:
            continue
        secs = f"{r['secs']:.1f}" if r["secs"] is not None else "-"
        print(f"{r['time']:<16} {r['surface']:<9} {r['input']:<40} {str(r['error']):<5} {secs:<7} {r['line']}")
    errors = [r for r in rows if r["error"]]
    longest = max((r["secs"] or 0 for r in rows), default=0)
    print(f"\n{tool}: {len(rows)} calls, {len(errors)} errors, longest {longest:.1f}s"
          + (f", since {args.since}" if args.since != "0000" else ""))
    by = collections.Counter((r["class"], r["time"][:10]) for r in errors)
    for (cls, day), n in sorted(by.items(), key=lambda kv: (kv[0][1], kv[0][0])):
        print(f"  {day} {cls} {n}")
    if errors:
        print(f"  last error {errors[-1]['time']}")


if __name__ == "__main__":
    main()

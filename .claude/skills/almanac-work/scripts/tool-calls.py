#!/usr/bin/env python3
"""Tabulate Brent's calls to one MCP tool from the transcripts brined-almanac pulls.

    python3 tool-calls.py triage_apply
    python3 tool-calls.py triage_apply --since 2026-11-07
    python3 tool-calls.py triage_apply --errors
    python3 tool-calls.py --list

Reads $BRINED_ALMANAC_STATE/transcripts, default ~/.local/share/brined-almanac/transcripts.
Prints one row per call: time, surface, input, outcome, seconds, and for an error its first line.
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

STATE = os.environ.get("BRINED_ALMANAC_STATE") or os.path.expanduser("~/.local/share/brined-almanac")
TRANSCRIPTS = os.path.join(STATE, "transcripts")
FENCE = re.compile(r"<<<EXTERNAL_UNTRUSTED_CONTENT[^>]*>>>.*?---\n(.*?)\n<<<END_EXTERNAL_UNTRUSTED_CONTENT", re.S)


def surface(path):
    name = os.path.basename(path)
    m = re.match(r"agent-main-([a-z]+)", name)
    return m.group(1) if m else name[:12]


def error_line(details):
    """First line of the error text. A nested-tool record keeps it under result.content, a plain
    toolResult record under content."""
    holder = details.get("result") if isinstance(details.get("result"), dict) else details
    try:
        text = holder["content"][0]["text"]
    except (KeyError, IndexError, TypeError):
        text = json.dumps(holder.get("content") or holder.get("result"))[:200]
    m = FENCE.search(text)
    if m:
        text = m.group(1)
    return text.strip().splitlines()[0][:120] if text.strip() else ""


def classify(line):
    if "-32001" in line:
        return "mcp-timeout"
    if "too many clients" in line:
        return "pg-too-many-clients"
    if "Approval timed out" in line or "card expired" in line:
        return "approval-timeout"
    if "approval" in line.lower():
        return "approval-unavailable"
    return "other"


def matches(name, tool):
    return name == tool or name.endswith(f"__{tool}")


def row(ts, path, inp, details, started):
    secs = (details.get("timestamp", 0) - started) / 1000 if started and details.get("timestamp") else None
    err = error_line(details) if details.get("isError") else ""
    return {
        "time": ts[:16],
        "surface": surface(path),
        "input": json.dumps(inp, separators=(",", ":"))[:40] if inp else "",
        "error": bool(details.get("isError")),
        "secs": secs,
        "line": err,
        "class": classify(err) if err else "",
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
                        rows.append(row(ts, path, d.get("input"), d, d.get("startedAt")))
                elif msg.get("role") == "toolResult" and matches(msg.get("toolName", ""), tool):
                    args, started = pending.pop(msg.get("toolCallId"), (None, 0))
                    if isinstance(args, str):
                        try:
                            args = json.loads(args)
                        except json.JSONDecodeError:
                            pass
                    rows.append(row(ts, path, args, msg, started))
    rows.sort(key=lambda r: r["time"])
    return rows


def list_tools():
    seen = collections.Counter()
    for path in glob.glob(os.path.join(TRANSCRIPTS, "*.jsonl")):
        with open(path, errors="replace") as fh:
            for line in fh:
                if "nested-tool" not in line:
                    continue
                m = re.search(r'"toolName":"([^"]+)"', line)
                if m:
                    seen[m.group(1)] += 1
    for name, n in seen.most_common():
        print(f"{n:6d}  {name}")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("tool", nargs="?", help="tool name, with or without the brineworks__ prefix")
    ap.add_argument("--since", default="0000", help="YYYY-MM-DD, keep calls on or after this day")
    ap.add_argument("--errors", action="store_true", help="rows for failed calls only")
    ap.add_argument("--list", action="store_true", help="list tool names seen and exit")
    args = ap.parse_args()
    if not os.path.isdir(TRANSCRIPTS):
        sys.exit(f"no transcripts at {TRANSCRIPTS}; set BRINED_ALMANAC_STATE or run the prod puller")
    if args.list:
        list_tools()
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

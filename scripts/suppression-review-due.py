#!/usr/bin/env python3
"""Report suppressions that are due for review, or that waive nothing.

Advisory by design: this always exits 0. The blocking checks live in
internal/secgov, and they check what is cheap and always true — that a
suppression is documented, and that it points at something git tracks.

Review age is deliberately not one of them. A timer that fails
`go test ./...` makes a governance deadline indistinguishable from a
broken build, blocks unrelated work, and makes bumping the date the
cheapest way out — which is the opposite of what a review gate is for.
The 2026-09-14 review is the evidence: it ran four months late, and what
it found was not age but drift — twelve entries addressed to paths that
had not existed since a refactor, and a rule-specific exclusion quietly
absorbing findings from a different rule family.

So: age is reported here as a prompt, and the two things that can be
measured rather than assumed are checked directly.
"""

from __future__ import annotations

import json
import os
import re
import sys
from datetime import date, datetime
from pathlib import Path

# The cadence from security/SUPPRESSION-GOVERNANCE.md. Reported, never
# enforced — see the module docstring.
REVIEW_INTERVAL_DAYS = 90

REVIEWED = re.compile(r"#\s*Last reviewed:\s*(\d{4}-\d{2}-\d{2})")
ENTRY = re.compile(r'^\s*-\s+["\']?([^"\'\n]+?)["\']?\s*$')

IN_ACTIONS = bool(os.environ.get("GITHUB_ACTIONS"))


def warn(msg: str) -> None:
    print(f"::warning::{msg}" if IN_ACTIONS else f"warning: {msg}")


def note(msg: str) -> None:
    print(msg)


def excl_reviews(path: Path) -> list[tuple[str, date]]:
    """Pair each scan.exclude entry with the review date above it."""
    if not path.exists():
        return []
    lines = path.read_text().splitlines()
    start = next((i + 1 for i, l in enumerate(lines) if l.strip().startswith("exclude:")), None)
    if start is None:
        return []
    out: list[tuple[str, date]] = []
    for i in range(start, len(lines)):
        line = lines[i]
        stripped = line.strip()
        if stripped and not line.startswith((" ", "\t")) and not stripped.startswith("-"):
            break
        m = ENTRY.match(line)
        if not m:
            continue
        for j in range(i - 1, start - 1, -1):
            c = lines[j].strip()
            if not c:
                break
            if ENTRY.match(lines[j]):
                continue
            if not c.startswith("#"):
                break
            if r := REVIEWED.search(c):
                out.append((m.group(1), datetime.strptime(r.group(1), "%Y-%m-%d").date()))
                break
    return out


def main() -> int:
    today = date.today()
    stale = 0

    for name, reviewed in excl_reviews(Path(".nox.yaml")):
        age = (today - reviewed).days
        if age > REVIEW_INTERVAL_DAYS:
            warn(f".nox.yaml exclude {name!r}: reviewed {reviewed} ({age}d ago, cadence {REVIEW_INTERVAL_DAYS}d)")
            stale += 1

    vex_path = Path("security/vex.json")
    waivers = []
    if vex_path.exists():
        waivers = json.loads(vex_path.read_text()).get("statements", [])
        for s in waivers:
            g = s.get("_governance") or {}
            raw = g.get("last_reviewed")
            if not raw:
                continue
            reviewed = datetime.strptime(raw, "%Y-%m-%d").date()
            age = (today - reviewed).days
            if age > REVIEW_INTERVAL_DAYS:
                warn(f"VEX {s.get('vulnerability')}/{str(s.get('_nox_fingerprint'))[:12]}: "
                     f"reviewed {reviewed} ({age}d ago, cadence {REVIEW_INTERVAL_DAYS}d)")
                stale += 1

    # A waiver whose fingerprint matches no finding waives nothing. That
    # is how eleven statements sat in this file until 2026-08-28 reading
    # as considered decisions while suppressing nothing at all: a nox
    # version bump changed the fingerprint scheme underneath them.
    dead = 0
    findings_path = Path("findings.json")
    if waivers and findings_path.exists():
        doc = json.loads(findings_path.read_text())
        live = {f.get("Fingerprint") for f in (doc.get("findings") or doc)}
        for s in waivers:
            fp = s.get("_nox_fingerprint")
            if fp and fp not in live:
                warn(f"VEX {s.get('vulnerability')}/{fp[:12]} matches no finding in this scan — "
                     f"it waives nothing. Delete it, or re-mint it from the scanner that gates CI.")
                dead += 1
    elif waivers:
        note("suppression-review: findings.json absent — skipped the dead-waiver check "
             "(run `nox scan .` first).")

    total = len(waivers) + len(excl_reviews(Path(".nox.yaml")))
    if stale == 0 and dead == 0:
        note(f"suppression-review: {total} suppressions, all within the "
             f"{REVIEW_INTERVAL_DAYS}d cadence and all live.")
    else:
        note(f"suppression-review: {stale} due for review, {dead} waiving nothing, of {total}.")
        note("This is advisory. See security/SUPPRESSION-GOVERNANCE.md for what a review is.")
    return 0


if __name__ == "__main__":
    sys.exit(main())

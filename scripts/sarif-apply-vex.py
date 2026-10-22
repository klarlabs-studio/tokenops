#!/usr/bin/env python3
"""Drop VEX-waived results from nox's SARIF before it reaches code scanning.

usage: sarif-apply-vex.py <in.sarif> <security/vex.json> <out.sarif>

Why not `nox scan --vex`: nox 1.48.1 matches an OpenVEX statement by its
`vulnerability` (the rule ID) and ignores `_nox_fingerprint`, so a single
AI-006 waiver hides every AI-006 finding in the repository, including ones
written after the waiver was reviewed. scripts/sec-gate.py has always
matched by fingerprint; this applies the same rule to the upload, so a
waiver covers exactly the finding it was written for and a new finding of
the same rule still opens an alert.

Only `status: not_affected` statements waive. An absent vex.json waives
nothing. A missing input SARIF exits 2: it means the scan did not write
one, and silently uploading nothing would read as a clean repository.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path


def waived_fingerprints(vex_path: Path) -> set[str]:
    """Fingerprints of the not_affected statements in an OpenVEX document."""
    if not vex_path.exists():
        return set()
    doc = json.loads(vex_path.read_text())
    return {
        s["_nox_fingerprint"]
        for s in doc.get("statements", [])
        if s.get("status") == "not_affected" and s.get("_nox_fingerprint")
    }


def is_waived(result: dict, waived: set[str]) -> bool:
    return any(fp in waived for fp in (result.get("fingerprints") or {}).values())


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        print(__doc__.strip().splitlines()[2], file=sys.stderr)
        return 2
    src, vex_path, out = (Path(a) for a in argv)
    if not src.exists():
        print(f"sarif-apply-vex: {src} missing — the scan wrote no SARIF", file=sys.stderr)
        return 2

    doc = json.loads(src.read_text())
    waived = waived_fingerprints(vex_path)
    kept = dropped = 0
    for run in doc.get("runs", []):
        results = run.get("results", [])
        remaining = [r for r in results if not is_waived(r, waived)]
        dropped += len(results) - len(remaining)
        kept += len(remaining)
        run["results"] = remaining

    out.write_text(json.dumps(doc))
    print(f"sarif-apply-vex: {kept} results to upload, {dropped} waived by fingerprint "
          f"({len(waived)} waivers in {vex_path})")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

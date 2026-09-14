# Suppression Governance

Every security-finding suppression in this repository carries a
documented rationale, a classification, and a review date. This document
defines what that means and what enforces it.

It is written for the maintainers who actually work on this repository —
currently a very small number — not for an audit committee. Where it says
"you", it means the person adding the suppression.

## Finding classification

| Category | Definition | Action |
|---|---|---|
| **Real Issue** | A genuine vulnerability, exposed secret, or policy violation that applies to this code. | Fix it. No suppression. |
| **Acceptable Pattern** | Technically accurate, but describes a deliberate design choice — a known-context secret in a test fixture, a permission a workflow genuinely needs. | Suppress with a documented rationale. |
| **False Positive** | The scanner confused a benign pattern with a signal — a semconv key read as an API key, prose in a comment read as a permission. | Suppress with a rationale explaining why the signal is absent. |
| **Deferred** | A real issue that is not immediately actionable. | File a GitHub issue, suppress with a reference to it. |

## The two mechanisms

### 1. `scan.exclude` in `.nox.yaml`

File-level exclusion, for when *every* finding in a file is the same
class of false positive. Each entry needs a preceding comment block with:

- what trips the detector in that file, and why the trigger is benign
- `# Classification:` — one of the four above
- `# Last reviewed:` — `YYYY-MM-DD`
- `# Transient:` — **only** for a generated artifact that is legitimately
  absent from a clean checkout (build output, scanner state). This exempts
  the entry from the liveness check below, so it has to be a written claim.

### 2. OpenVEX statements in `security/vex.json`

Per-finding, for when a file contains both real and spurious signals.
Each statement needs `vulnerability`, `status: not_affected`, an OpenVEX
`justification`, a specific `impact_statement` (not a template), the
`_nox_fingerprint`, and a `_governance` block with `classification`,
`last_reviewed`, and `reviewed_by`.

**Mint fingerprints from the scanner that gates CI**, not from your local
one. CI pins `NOX_VERSION` in `.github/workflows/security.yml`; a local
install is usually ahead of it, and the two disagree about what they
report. A fingerprint from the wrong version waives nothing in the run
that matters. Take them from the `nox-findings` artifact of a main-branch
run.

## What is enforced, and what is not

**Blocking** (`internal/secgov`, runs in `go test ./...`):

- every suppression is documented, with a valid classification and a
  parseable review date
- every `scan.exclude` entry points at something **git tracks**, unless it
  declares `# Transient:`

Both are cheap, deterministic, and always true — they don't depend on the
date, on local build state, or on a scan having run.

**Advisory** (`scripts/suppression-review-due.py`, `make sec-review`, and a
non-blocking step in the security workflow):

- suppressions past the 90-day review cadence
- VEX statements whose fingerprint matches no finding in the current scan

These warn. They do not fail the build.

### Why age is not a blocking check

It used to be: a 120-day ceiling that failed `go test ./...`. It expired on
2026-09-07 and left `main` red for a week, blocking unrelated work, until
someone had time to do a security review. The cheapest way out of that
state is to bump the date — so a gate built to prevent rubber-stamping had
made rubber-stamping the path of least resistance.

It was also measuring the wrong thing. When the review finally ran, what it
found was not age. It was drift: twelve entries addressed to paths that had
not existed since the DDD refactor, and a workflow exclusion written for one
rule quietly absorbing findings from another. **Drift is directly
measurable, and measuring it directly catches it the day it happens rather
than at the next timer.** That is what the liveness checks do.

The cadence still exists, because re-reading a rationale against the current
scanner is worth doing on a schedule. It just prompts instead of blocking.

## What a review actually involves

1. Re-scan with the current nox.
2. For each suppression, check the *claim*, not the date — copy the excluded
   path into a bare tree with no `.nox.yaml`, scan it, and see whether it
   still produces what the comment says it produces. Rules get retired and
   files get moved.
3. Delete what no longer suppresses anything. A suppression that suppresses
   nothing is worse than none: it reads as a considered decision.
4. Update `Last reviewed:` on what remains.

## Review log

**2026-09-14 — reviewed every entry against what it suppresses today.**
Prompted by the 120-day timer expiring, which had been failing `main` for a
week. Each of the 27 excluded paths was copied into a bare tree and
rescanned under nox 1.35.0.

Fourteen produced nothing at all. Twelve pointed at paths that had not
existed since the DDD refactor moved those packages under
`internal/contexts/` — inert for months while still reading as active
policy. All were removed rather than re-dated. `redactor_test.go` was
repointed and kept: it still produces 23 findings from the sample AWS keys
the redaction tests embed, and is the only source-file exclusion that still
earns its place.

`.github/workflows/*.yml` was dropped entirely. It had been excluded for
SEC-659/697, which no longer fires there — what it was actually absorbing
was nine Infrastructure findings from a different rule family, on the
workflow files, through a month of workflow security work. Those are now
triaged into `vex.json`.

The timer was replaced by the liveness checks described above, on the
reasoning in "Why age is not a blocking check".

**2026-08-28 — all suppressions removed.** The scanner was pinned at nox
0.9.5, which reported 1386 findings including 11 criticals, every one of
them the typosquat detector firing on a well-known package (`vue` as a
typosquat of `vite`, in a repo whose dashboard is written in Vue). All
eleven VEX statements existed to waive those.

nox 1.30.1 reports 523 findings, **0 critical**, and 1 typosquat finding.
Its fingerprint scheme also changed, so none of the eleven statements
matched anything any more — they were dead entries, not active
suppressions. Removed rather than migrated.

`security/vex.json` was therefore removed, not emptied — `internal/secgov`
rejects an empty statements list, and `scripts/sec-gate.py` treats an absent
file as "no waivers in effect". It was recreated on 2026-09-14 when there
were real waivers to record.

Worth noting in hindsight: this review touched `vex.json` and left every
`.nox.yaml` review date at `2026-05-10`, already past the cadence. A written
process did not catch what a test later did.

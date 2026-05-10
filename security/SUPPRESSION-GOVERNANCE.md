# Suppression Governance

Every security-finding suppression in this repository carries a
documented rationale, a classification, a review date, and an owner.
This document defines the governance model.

## Finding Classification

Every finding the scanner emits is classified into one of four
categories. The classification determines what action is required.

| Category | Definition | Action |
|---|---|---|
| **Real Issue** | A genuine vulnerability, exposed secret, or policy violation that applies to the code as it exists in this repository. | Fix the underlying cause. No suppression. |
| **Acceptable Pattern** | A finding that is technically accurate but describes a deliberate design choice (e.g., a known-context secret embedded in a test fixture). | Suppress with documented rationale. Classify in VEX or exclude comment. |
| **False Positive** | A finding that is incorrect — the scanner confused a benign pattern with a signal (e.g., an npm package name flagged as typosquat, an OpenTelemetry semconv key flagged as an AI provider key). | Suppress with documented rationale explaining why the signal is absent. |
| **Deferred** | A real issue that is not immediately actionable (e.g., requires a dependency upgrade that introduces a breaking change, or needs a larger refactor). | File a GitHub issue and suppress with a reference. Assign a review-by date. |

## Suppression Mechanisms

There are two suppression paths:

### 1. `scan.exclude` in `.nox.yaml`

Used for file-level exclusions where **every finding** in the file is
the same class of false positive. Each entry **must** have a
preceding YAML comment block explaining:

- What triggers the detector in that file
- Why the trigger pattern is benign
- The classification (Acceptable Pattern or False Positive)
- The last review date

Example:

```yaml
    # This test fixture intentionally embeds sample AWS keys to exercise
    # the redaction package's secret-detection logic.
    # Classification: Acceptable Pattern
    # Last reviewed: 2026-05-10
    - internal/redaction/redactor_test.go
```

### 2. OpenVEX statements in `security/vex.json`

Used for per-finding false positives where the same file may contain
both real and spurious signals. Each VEX statement **must** include:

| Field | Requirement |
|---|---|
| `vulnerability` | The scanner rule ID (e.g. `VULN-002`, `SEC-569`). |
| `status` | `not_affected` for all suppressible classifications. |
| `justification` | One of the [OpenVEX justification values](https://github.com/openvex/spec/blob/main/OPENVEX-SPEC.md). |
| `impact_statement` | A specific, contextual explanation — not a generic template. |
| `_nox_fingerprint` | The finding fingerprint from the scan output. |
| `_governance` | Object with `classification`, `last_reviewed`, and `reviewed_by`. |

Example:

```json
{
  "vulnerability": "VULN-002",
  "status": "not_affected",
  "justification": "vulnerable_code_not_present",
  "impact_statement": "False positive. 'vue' is the official Vue.js framework, not a typosquat of 'vite'. Both are first-party packages from the Vue/Vite organisations.",
  "products": ["github.com/felixgeelhaar/tokenops"],
  "_nox_fingerprint": "1f4dff23501a9568fbc5c744257f8e03c7e0ec819cc9583c2d5bc764539594d3",
  "_governance": {
    "classification": "False Positive",
    "last_reviewed": "2026-05-10",
    "reviewed_by": "tokenops-maintainers"
  }
}
```

## Review Cadence

| Suppression type | Review interval | Trigger |
|---|---|---|
| `scan.exclude` entry | Every 90 days | Audit checklist item in quarterly security review |
| OpenVEX waiver | Every 90 days | Audit checklist item in quarterly security review |
| Deferred (GitHub issue) | Per issue due date | GitHub issue reminder |

A quarterly review **must**:

1. Re-scan the repository with the latest nox version.
2. Re-evaluate every suppression — the scanner may have been fixed,
   the false-positive rule may have been retired, or the excluded
   code may have changed.
3. Remove suppressions that are no longer needed.
4. Update `last_reviewed` timestamps for suppressions that remain
   valid.

## Enforcement

The CI gate (`scripts/sec-gate.py`) blocks the build on **any
unwaived critical** finding. It does not enforce the governance
metadata above — that is enforced through code review.

Reviewers **must** reject PRs that:

- Add a `scan.exclude` entry without a rationale comment.
- Add a VEX statement without an `impact_statement` that clearly
  explains why this finding does not apply.
- Introduce a suppression with `classification: Deferred` but no
  linked GitHub issue.

## Owner

The `tokenops-maintainers` group is the owner of all suppressions.
Individual entries may delegate ownership to a specific team member
via the `reviewed_by` field, but the group retains overall
accountability.

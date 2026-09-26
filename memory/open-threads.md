---
updated: 2026-09-26
---
## Open

- Subscription-plan telemetry: validate canonical GPT Plus/Pro and Claude
  Pro/Max/Business/Team/Enterprise semantics with genuine plan-meter records.
  The supported API-backed cohorts prove routing and metered cost only; API
  billing is separate from subscription quota accounting. Current Codex data
  reports opaque plan type `prolite` with one weekly window; do not map it to a
  catalog plan without authority. Claude JSONL lacks an authoritative quota
  percentage. A content-safe authenticated Claude UI check confirms the
  general session, aggregate-weekly, and model-specific-weekly shape, but
  persistent ingestion remains unconnected because the HttpOnly browser cookie
  could not be exported. The decoder now preserves shape-valid vendor window
  labels instead of assuming its historical `seven_day_opus` name; capture an
  actual response content-safely before claiming live model-window ingestion.
- Relicta release governance: resolve or guard the observed `gitsign: true`
  unsigned-tag behavior and `autocommitchangelog: false` local append before
  the next release.
- fmt learning threshold tuning: the merged provenance guard distinguishes
  actual wrapped runs from offline projections and prevents up-tuning without
  recovery reads. It still needs enough genuine recovery telemetry.

## Deferred by policy

- Background coaching pipeline: retain on-demand coaching until opt-in,
  replay scope, and cost policy are explicit.
- Coach-hook Phase 2+, extra pricing sources, OpenAI-compatible live provider
  validation, and further CI-minute reductions remain optional follow-ups;
  they are not current roadmap gates.

## Resolved 2026-09-25

- #387 removed demo and browser dashboard surfaces.
- #388 deduplicated repeated measurement caveats.
- #389 corrected unpriced-plan spend wording.

## Resolved 2026-09-26

- Installed v0.72.1 health/readiness and release identity passed after the
  Homebrew upgrade.
- Five-pair OpenAI and Anthropic API-backed coding-agent cohorts reached
  supported evidence with independent verifiers and stable multi-call arms.

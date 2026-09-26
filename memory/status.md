---
updated: 2026-09-26
---
## Current State

TokenOps is a local-first adaptive control plane for AI-assisted work. Current
source `main` is `d664208` (PR #389). The CLI/MCP/API surfaces are the product;
there is no bundled browser dashboard or demo-data workflow (PR #387).

ADR 0004 Phases 0–4 and 6–9 are implemented. Phase 5 implementation is shipped,
but real outcome validation remains open: the observed session data has no
matched intervention/outcome cohort, so verification must remain observational.
The plan state is tracked in `.roady/`; current intent and phase truth are in
`docs/adr/0004-ai-work-intelligence-and-control.md`.

Last live validation (2026-09-25) exercised CLI and MCP against actual local
session telemetry, without adding demo data. It found and fixed repeated
measurement caveats (#388) and inaccurate unpriced-plan spend wording (#389).
The host-installed CLI/daemon was v0.68.1 at that time; it has not been
upgraded by this work.

## Next

1. On an operator-controlled deployment, upgrade the installed CLI/daemon to
   current source and repeat the CLI/MCP checks.
2. Let real work produce a genuine intervention and outcome. Record only a
   human or recognized verifier assessment actually observed; do not seed
   example activity or claim causal uplift before matched evidence exists.
3. Keep background coaching off until its opt-in, scope, and cost policy are
   explicit; on-demand coaching remains the safe path.

## Recently Resolved

- PR #387: removed demo command/data and browser dashboard surfaces.
- PR #388: deduplicated repeated provenance caveats in aggregates.
- PR #389: distinguished incomplete API-equivalent estimates from actual
  plan-covered marginal cost.

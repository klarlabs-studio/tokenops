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

Live validation on 2026-09-26 restarted the installed supervised daemon and
confirmed CLI health/readiness plus an MCP initialize, tools/list, and
`tokenops_status` call against the real local store. No demo/seed command was
run. Installed CLI/daemon is v0.70.0, but it still exposes the retired
`demo` command and `tokenops_dashboard` MCP tool; it predates source cleanup in
PR #387 and is not yet the current product surface. `tokenops events` used its
JSONL fallback and reported 274 `budget.exceeded` events; this is not a matched
intervention/outcome cohort and cannot establish quality or causal uplift.

The daemon was not answering initially. A supervised restart succeeded from
the host context, and health/readiness subsequently returned 200. The local
MCP stdio validation also returned ready status on v0.70.0.

A temporary build of current checkout (not installed) also passed CLI health
checks and MCP stdio initialize/tool-list/status/resource-glance calls against
that daemon. The resource glance read actual Claude Code and Codex local
session records, reported current pressure as clear, and recommended
continuing. This validates the surface and data path only; it does not supply
an intervention/outcome pair or verify work quality.

## Next

1. On an operator-controlled deployment, upgrade to a release containing
   PR #387 and repeat CLI/MCP checks; confirm the retired demo/dashboard
   surfaces are absent.
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

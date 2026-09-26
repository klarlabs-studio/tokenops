---
updated: 2026-09-26
---
# Current Roadmap Snapshot

Canonical sequencing: `docs/adr/0004-ai-work-intelligence-and-control.md`.
Roady implementation status: `.roady/plan.json` + `.roady/state.json`.

## Shipped

- Phases 0–4: privacy/safety, measurement provenance, freshness, work model,
  and interface-independent capabilities (#339–#343).
- Phase 5 implementation: outcome/intervention identity, unified authority,
  and comparison surfaces (#345, #347). **Real causal evaluation is not yet
  demonstrated** because there are no matched real intervention/outcome pairs.
- Phases 6–9: canonical events (#361–#369), runtime lifecycle + installed
  binary journey (#349, #370–#378), intent-oriented MCP (#380–#382), and
  surface-native insights (#383–#386).
- Production-only cleanup and live-data clarity fixes: #387–#389.

## Active Validation

- Host CLI/MCP validation succeeded on v0.70.0 after restarting its supervised
  daemon. That binary still exposes demo/dashboard entry points removed by
  PR #387; upgrade and verify a release containing that cleanup.
- The local JSONL fallback reported 274 `budget.exceeded` events, which is
  insufficient evidence for the still-open Phase 5 outcome cohort.
- Accumulate real, outcome-linked work and evaluate only when evidence supports
  a matched comparison. No demo seeding and no implied success from completion.

## Deferred

- Daemon-started background coaching: keep on-demand behavior until opt-in,
  replay scope, and cost policy are explicitly designed.
- fmt learning threshold tuning: still needs real command-run telemetry.

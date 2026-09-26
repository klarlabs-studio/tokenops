---
updated: 2026-09-26
---
# Current Roadmap Snapshot

Canonical sequencing: `docs/adr/0004-ai-work-intelligence-and-control.md`.
Roady implementation status: `.roady/plan.json` + `.roady/state.json`.

## Shipped

- Phases 0–4: privacy/safety, measurement provenance, freshness, work model,
  and interface-independent capabilities (#339–#343).
- Phase 5 implementation and bounded live validation: outcome/intervention
  identity, unified authority, comparison surfaces, and supported five-pair
  API-backed Codex and Claude Code cohorts (#345, #347, #394–#409). These are
  scoped randomized results, not universal model-quality claims.
- Phases 6–9: canonical events (#361–#369), runtime lifecycle + installed
  binary journey (#349, #370–#378), intent-oriented MCP (#380–#382), and
  surface-native insights (#383–#386).
- Production-only cleanup and live-data clarity fixes: #387–#389.

## Active Validation

- Installed v0.72.1 reports its tagged version/commit and passes host-local
  health/readiness. Read-only MCP status/resource-glance also passed against
  genuine local Codex and Claude Code records.
- The OpenAI and Anthropic five-pair coding-agent cohorts both reached
  `supported` with independent verifier outcomes, stable multi-call arms,
  provider usage, latency, cost, and content-safe tool-call evidence.
- Validate subscription-plan telemetry separately with genuine usage-meter
  records. API-backed routing cannot establish Plus, Pro, Max, Business, Team,
  or Enterprise quota semantics.
- Accumulate additional outcome-linked work only for new task classes or policy
  questions. No demo seeding and no implied generalization from completion.

## Deferred

- Daemon-started background coaching: keep on-demand behavior until opt-in,
  replay scope, and cost policy are explicitly designed.
- fmt learning threshold tuning: guard is in place, but still needs enough
  genuine wrapped-run and recovery telemetry; no threshold experiment yet.

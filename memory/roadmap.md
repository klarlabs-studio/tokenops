---
updated: 2026-09-27
---
# Current Roadmap Snapshot

Canonical sequencing: `docs/adr/0004-ai-work-intelligence-and-control.md`.
Roady implementation status: `.roady/plan.json` + `.roady/state.json`.

## Shipped

- Hook parity (#265, closed 2026-09-27): coach-hook on Claude Code, Codex,
  Cursor, and opencode; read-guard on Claude Code and opencode, permanently
  unavailable on Codex and Cursor (#274, #277, #282). The Codex hook is
  trusted and firing on the maintainer's machine; Cursor is unit-tested only.

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
- v0.74.0: prepared workflow IDs through the Claude bridge (#427) and
  generation-only Anthropic metering (#428). ADR 0004 is exhausted.
- v0.74.1: no event loss under store lock contention and owner-only store
  permissions (#431).
- v0.74.2: pollers skip already-stored events on restart (#432).
- v0.74.3: API token required without storage (#433); unknown smart-routing
  interventions rejected (#434).
- v0.75.0: per-process slow write-lock diagnostics (#436); release runs the
  config matrix (#435).

## Validated

- Installed v0.75.0 reports its tagged version/commit and passes host-local
  health/readiness. A bounded Claude Code run through the v0.74.0 bridge
  returned `measured` review evidence against the exact prepared workflow ID. A fresh read-only Codex session used the installed MCP
  binary to complete the `tokenops_prepare_work` to `tokenops_review_work`
  handoff with exact workflow-ID preservation and truthful `no_evidence`
  semantics for work that was not executed.
- The OpenAI and Anthropic five-pair coding-agent cohorts both reached
  `supported` with independent verifier outcomes, stable multi-call arms,
  provider usage, latency, cost, and content-safe tool-call evidence.
- The configured Claude Max plan has genuine supervised usage-meter records.
  Codex JSONL and the documented `account/read` method both report the opaque
  OpenAI identity `prolite`; official documentation publishes no public-plan
  mapping, so TokenOps preserves it separately from canonical `gpt-plus`.

## Proposed Next

- The blocked outcome cohort and the unvalidated subscription plans need
  evidence from real work and accounts beyond this machine.
- Waste-detector findings that name a cause and a dollar amount, toward
  ADR 0005's build gate; delete the never-wired async coaching pipeline.
- Team plane (#250) stays deferred until solo users ask for it.

## Active Validation

- Validate additional subscription plans only with genuine usage-meter records
  from accounts carrying them. API-backed routing cannot establish Plus, Pro,
  Max, Business, Team, or Enterprise quota semantics.
- Accumulate additional outcome-linked work only for new task classes or policy
  questions. No demo seeding and no implied generalization from completion.

## Deferred

- Daemon-started background coaching: ADR 0005 accepted with a build gate;
  on-demand behavior stays until three finding kinds earn the digest.
- fmt learning threshold tuning: the 2026-09-27 audit found 14 genuine wrapped
  runs and zero recovery reads. The remaining 33,778 runs are session-log
  projections and cannot establish re-access safety, so no threshold experiment
  or configuration change is justified yet.

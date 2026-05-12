# Changelog

## Unreleased

### Added

- `tokenops demo` now seeds `OptimizationEvent`s alongside prompts
  (~40% rate, 20–40% savings) so the first-run scorecard reflects a
  realistic optimizer mix and TEU lifts off zero. Demo output reports
  prompts vs. optimizations separately.
- Scorecard `KPIResult` gained `name` + `description` fields so
  operators can decode the FVT / TEU / SAC abbreviations inline.
  `tokenops scorecard` text output adds a Definitions block.
- Roady backlog: new `Plan-Based Cost Model` feature spec covering
  Claude Max / ChatGPT Plus / Copilot / Cursor flat-rate plans
  (cost_source on PromptEvent, plan quota tracking, headroom metrics).
  Implementation deferred to its own cycle.

## 0.3.1 - 2026-05-12

### Fixed

- `tokenops_status` returned `state: "not_ready"` when MCP serve mode
  was actually ready but the on-disk config still listed disabled
  subsystems. New `degraded` state distinguishes "ready with reduced
  surface area" from "broken".

## 0.3.0 - 2026-05-12

First-run activation and security-suppression governance.

### Added

- **`tokenops init`** scaffolds an opinionated config (sqlite storage
  + rules subsystem enabled at `$PWD`) at `$XDG_CONFIG_HOME/tokenops/
  config.yaml`. Idempotent; `--force` overwrites, `--print-only`
  emits YAML to stdout without touching disk.
- **`tokenops demo`** seeds deterministic synthetic events
  (5 providers/models, 4 workflows, 3 agents, jittered tokens + cost)
  so `tokenops spend`, `tokenops scorecard`, `tokenops forecast`, and
  the MCP analytics tools return populated data on a fresh install.
  Flags: `--days`, `--per-day`, `--reset`, `--dry-run`, `--seed`.
- **Status blockers / next-actions**: `tokenops_status` MCP tool and
  the daemon's `/readyz` endpoint now expose `blockers[]`
  (`storage_disabled`, `rules_disabled`, `providers_unconfigured`) and
  `next_actions[]` so first-run callers see exactly what to fix
  without grepping config. `config.Blockers()` + `NextActionsFor()`
  are the canonical helpers.
- **Disabled-subsystem error contract**: daemon analytics + rules
  routes (`/api/spend/*`, `/api/optimizations`, `/api/audit`,
  `/api/rules/*`) now return `503 {error, hint}` when their
  subsystem is off, instead of an opaque `404`.
- **Suppression governance gate** (`internal/secgov`): `go test`
  now enforces that every entry in `security/vex.json` carries
  `_governance.{classification, last_reviewed, reviewed_by}` and
  every `.nox.yaml` `scan.exclude` entry is preceded by the same
  metadata in comments. Review age capped at 120 days.

### Changed

- `security/vex.json` waivers gain `_governance` metadata on all
  eight existing statements; bumped doc version to 2.
- README `Getting started` is now a 3-command quickstart
  (`init` → `demo` → `start`) plus a first-run troubleshooting
  table indexed by blocker identifier.

## 0.2.0 - 2026-05-12

The Rule Intelligence wedge lands plus a full DDD refactor: rule
artifacts (`CLAUDE.md`, `AGENTS.md`, Cursor rules, MCP policies) become
first-class operational telemetry, repository layout reorganises around
bounded contexts, and the MCP / HTTP / CLI surfaces all share the same
domain services. Adopts felixgeelhaar/{bolt, fortify v1.5.0, mcp-go}.

### Added

- **Rule Intelligence** (issue #12): full subsystem treating
  `CLAUDE.md`, `AGENTS.md`, Cursor rules, MCP policies, and repo
  conventions as first-class operational telemetry.
  - `RuleSourceEvent` + `RuleAnalysisEvent` payloads (schema 1.1.0).
  - Analyzer (per-section token cost + density), Compressor (Jaccard
    near-duplicate pruning + quality gate), Router (dynamic injection
    with token + latency budgets), ROIEngine, Benchmark, Conflicts
    detector (redundant / drift / anti-pattern).
  - CLI: `tokenops rules analyze|conflicts|compress|inject|bench`.
  - MCP: `tokenops_rules_*` tools.
  - HTTP: `/api/rules/*` endpoints with cache invalidated by
    `RuleCorpusReloaded` domain event.
  - Vue dashboard `/rules` view.
- **Domain event bus** (`internal/domainevents`): typed cross-context
  pub/sub with async mode, bounded queue, panic recovery,
  cancellable subscriptions, slow-handler detection, drop / panic /
  dispatch counters. JSONL persistence with size-based rotation,
  fsync on close, lenient replay.
- **Telemetry contracts** + golden tests pinning the on-wire JSON for
  every envelope payload (`pkg/eventschema/golden_test.go`).
- **DDD architecture** (`docs/architecture-ddd.md`): bounded contexts,
  ubiquitous language glossary, layering rules. Enforced via
  `internal/archlint` — `go list -deps` based test fails CI when a
  domain package imports an adapter or undocumented infrastructure.
- **Composition root** `internal/bootstrap`: single construction site
  for sqlite store, spend engine, analytics aggregator, tokenizer
  registry, redactor, domain bus, event counter.
- **Eval gate** (`internal/contexts/optimization/eval`): regression
  thresholds on success rate, per-optimizer quality drift, optimizer
  presence. CLI: `tokenops eval [--baseline --enforce --output]`.
- **Wedge KPI scorecard** wired to live event store (FVT/TEU/SAC).
- **Coverage debt** (`internal/contexts/governance/coverdebt`):
  risk-weighted coverage report from Go cover profile.
- **Audit subscriber** wires `BudgetExceeded` + `OptimizationApplied`
  events to the audit log with bounded concurrency, drop counter.
- **`tokenops audit`** and **`tokenops events`** CLIs (with JSONL
  fallback when daemon unreachable, `--since` filter, URL-scheme
  aware).
- **`tokenops_domain_events`** MCP tool surfaces in-process event
  counts + audit drop counter.
- **fortify v1.5.0 adoption**: provider proxy routes can opt into
  `CircuitBreakerStream` via a new `resilience.*` config block. Each
  upstream gets its own circuit breaker plus FirstByte / Idle / Total
  watchdogs; stalled SSE streams surface as `504 Gateway Timeout`
  instead of hanging the client, and a flaky vendor trips without
  taking siblings offline. Off by default (no behaviour change for
  existing deployments). OTLP exporter wraps the upstream call in a
  fortify circuit breaker for finite-response fault tolerance.
- **bolt logger adoption**: `observ.NewLogger` now produces zero-alloc
  JSON via `github.com/felixgeelhaar/bolt` when `log.format=json`;
  text format retains stdlib slog.
- **mcp-go adoption**: `internal/mcp` is now a thin adapter over
  `github.com/felixgeelhaar/mcp-go`. JSON-RPC framing, schema
  generation, and stdio transport move upstream; every tool gets a
  typed input struct with auto-generated JSON schema. CLI `tokenops
  serve` calls `mcp.ServeStdio` instead of the prior handwritten loop.

### Changed

- Schema version 1.0.0 → 1.1.0 (additive: rule_source + rule_analysis
  event kinds, tokenops.rule.* OTLP attributes).
- Repository layout: domain packages moved under
  `internal/contexts/<ctx>/<pkg>`; `internal/infra/rulesfs/` carries
  filesystem-touching rule corpus loader.
- Config snapshot (`config.Config.Snapshot`) redacts OTel headers.
- Bus.Subscribe returns `*Subscription` with `Cancel()`.

### Fixed

- Bus close/publish race (queueClosed guard).
- Audit subscriber goroutine leak past shutdown.
- JSONLog rotation size tracked via `Stat`, no longer estimated.
- Daemon shutdown bounded by `cfg.Shutdown.Timeout` for both telemetry
  and domain bus drains.
- `LoadCorpus` deduplicates `RuleCorpusReloaded` events when the
  corpus digest hasn't changed.

# Changelog

## Unreleased

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

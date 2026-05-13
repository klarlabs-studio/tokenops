# Changelog

## Unreleased

## 0.9.1 - 2026-05-13

### Added

- `tokenops demo --reset-only`: purges `source=demo` events without
  reseeding. Closes the gap that forced operators into raw SQL when
  they wanted to clean leftover seeded data. Idempotent.

## 0.9.0 - 2026-05-13

### Added

- **`signal_quality` on every session_budget and plan_headroom**:
  closed-set `level` (low|medium|high), `source`
  (mcp_tool_pings|proxy_traffic|vendor_usage_api), one-sentence
  `caveat`, and `upgrade_paths` so callers see exactly how trustworthy
  the underlying number is. Default response leads with
  `level: low, source: mcp_tool_pings` and a disclaimer.
- **Empty-state scorecard**: when no KPI has real-data backing, the
  scorecard returns `OverallGrade: warming_up` plus a 3-step
  activation checklist instead of a misleading `F`. CLI text
  renderer special-cases the warming-up state.
- **Data-warning banner on cost/headroom responses**: when synthetic
  events make up more than 10% of the queried window,
  `tokenops_spend_summary` / `tokenops_plan_headroom` /
  `tokenops_session_budget` attach a `data_warning` object with the
  ratio, real/demo counts, and the exact reset command.
- **Hot-reload on `tokenops plan set`**: `tokenops serve` polls the
  resolved config path every 2 seconds and swaps the snapshot
  atomically on mtime change. `PlanDeps.ConfigGetter` plumbs the
  live snapshot to every plan tool — operators no longer need to
  reconnect their MCP host after `tokenops plan set`.
- **Catalog-alias migration shim**: `plans.ResolveAlias` maps
  retired catalog names to modern entries. `tokenops plan set
  claude-max` prints `renamed claude-max -> claude-max-20x` and
  writes the modern name. Stale docs / blog posts keep working.
- **Launch plan + tracker docs**:
  `docs/launch-plan.md` (Loom script, Show HN post, Discord posts,
  founder-DM template, success criteria) and
  `docs/launch-tracker.md` (10-row tracker, per-call notes,
  synthesis rubric, negative-signal log) so the maintainer can run
  the GTM cycle from a single doc.

## 0.8.1 - 2026-05-13

### Fixed

- Scorecard compute path now filters Source=demo envelopes before
  computing FVT/TEU/SAC. v0.8.0 added the isolation everywhere
  except this query path so `tokenops demo` data continued to
  inflate the wedge KPIs.

## 0.8.0 - 2026-05-13

### Added

- **Demo data isolation**: synthetic events seeded by `tokenops demo`
  are now excluded from every default analytics surface (`spend
  summary`, `top consumers`, `burn rate`, `forecast`, plan headroom +
  session budget). Opt back in with `include_demo: true` on the MCP
  tool input. `analytics.DefaultExcludedSources` is the single source
  of truth; pass `ExcludeSources: []string{}` to bypass.
- **`tokenops_data_sources` MCP tool**: groups events by source
  column (`proxy`, `mcp-session`, `demo`, `otlp`, …) so operators see
  at a glance whether headroom + spend math run on real or seeded
  data.
- **MCP session middleware**: every `tools/call` request increments
  `session.Tracker` regardless of which handler runs. Replaces the
  per-tool Record sites in `plan_headroom` / `session_budget` so the
  window-count signal is uniform across the surface.
- **`tokenops_help` MCP tool**: 6-category curated index (setup,
  session, cost, workflows, rules, debug) so agents and operators
  can navigate the 20+ tool surface without enumerating
  `tools/list`.

### Fixed

- **Rules walker**: `filepath.WalkDir` callback now tolerates
  `fs.ErrPermission` (skips the offending subtree / file) and
  `fs.ErrNotExist` (race between dir-listing and stat).
  `tokenops rules analyze --root ~/.claude` no longer aborts with
  `permission denied` from `~/Library/Saved Application State` and
  friends. Skip list extended to Library/Containers/.Trash.

## 0.7.1 - 2026-05-13

### Fixed

- `tokenops plan list` (and other read-side subcommands routed via
  `loadConfig`) returned "no plans configured" right after `tokenops
  plan set` because the loader honoured only `--config` and env vars
  while the mutation verbs defaulted to the XDG path. `loadConfig`
  now auto-discovers `$XDG_CONFIG_HOME/tokenops/config.yaml` (or
  `~/.config/tokenops/config.yaml`) when `--config` is unset.
- Empty-state hint on `plan list` now points at `tokenops plan set …`
  instead of the legacy env-var instructions.

## 0.7.0 - 2026-05-12

### Added

- **MCP-first wedge**: TokenOps now observes operator activity inside
  the MCP session rather than relying on proxy traffic, which the
  three-skill review confirmed is the wrong consumption surface for
  flat-rate Claude Code / Cursor users.
  - `internal/contexts/spend/session` package: `Tracker.Record` emits
    a plan_included synthetic `PromptEvent` for every observed MCP
    tool invocation, so `ConsumptionInWindow` / `headroom` see real
    activity without a proxy.
  - `tokenops_session_budget` MCP tool: predicts whether the current
    session will hit the rate-limit cap; returns
    `recommended_action ∈ {continue, slow_down, switch_model,
    wait_for_reset}` with a confidence band.
  - `plans.ComputeSessionBudget` pure function with 7 unit tests
    covering the recommendation matrix.
- **Config-as-code primitive**:
  - `tokenops plan set <provider> <plan>` / `tokenops plan unset`
    replace the previous JSON-edit-the-MCP-host-config flow.
  - `tokenops provider set|unset|list` mirrors the same verb shape.
  - Shared `config_mutate.go` helpers (`readMutableConfig`,
    `writeMutableConfig`) reusable for future `tokenops <subsystem>
    set` commands.
- **Hint sweep**: every structured `{error, hint}` payload now
  contains the exact corrective command (`tokenops plan set …`,
  `tokenops provider set …`) instead of an environment-variable name.
- **Customer discovery scaffolding**: `docs/customer-discovery.md`
  with a 9-question Torres-style interview script, recruitment
  targets, synthesis rubric, and reject criteria for the 5-user
  wedge validation sprint.

### Changed

- README quickstart replaces the env-var / JSON-edit instructions
  with `tokenops plan set anthropic claude-max-20x` (etc.).
- `docs/plan-cost-model.md` notes the proxy is no longer the primary
  observation surface; MCP-side activity is the new default.

## 0.6.0 - 2026-05-12

### Added

- **Rate-limit window headroom** for subscription plans that publish
  rolling windows instead of monthly token caps.
  - `Plan` gains `MessagesPerWindow` + `WindowUnit` fields; catalog
    splits generic `claude-max` into `claude-max-5x` (50 msgs / 5h)
    and `claude-max-20x` (200 msgs / 5h). Adds documented caps for
    `claude-pro`, `gpt-plus`, `gpt-team`.
  - `HeadroomReport` gains `window_cap`, `window_consumed`,
    `window_pct`, `window_resets_in`, `window_unit` fields.
    `overage_risk` headline takes the worst of monthly and window
    signals.
  - `tokenops plan headroom` text output prints both monthly and
    window lines; `tokenops_plan_headroom` MCP tool exposes the same
    fields.
  - `internal/contexts/spend/plans.ConsumptionInWindow` reader counts
    plan-included PromptEvents over a trailing window.

### Changed

- Generic `claude-max` removed from the plan catalog. Users on the
  Anthropic Max plan should pick `claude-max-5x` or `claude-max-20x`
  depending on their tier.

## 0.5.0 - 2026-05-12

### Added

- **Plan-Based Cost Model**: subscription-aware spend tracking for
  Claude Max / Pro, Claude Code Max / Pro, ChatGPT Plus / Pro / Team,
  GitHub Copilot Individual / Business, Cursor Pro / Business.
  - `PromptEvent.CostSource` enum (`metered` default,
    `plan_included`, `trial`); schema bumped to 1.2.0.
  - `internal/contexts/spend/plans` package: catalog with dated
    `SourceURL` per plan, `ComputeHeadroom` returning
    `consumed_pct` / `headroom_days` / `overage_risk` (low / medium
    / high / unknown), and `ConsumptionFor` reader.
  - `tokenops plan list|headroom|catalog` CLI subcommands and
    `tokenops_plan_headroom` MCP tool.
  - `Config.Plans` map (`plans:` YAML block or
    `TOKENOPS_PLAN_<PROVIDER>` env) validated against the catalog.
  - `tokenops demo --plan <name>` stamps PromptEvents with
    `cost_source=plan_included` so the headroom surface populates on
    a fresh install.
  - `docs/plan-cost-model.md` documents the catalog and add-a-plan
    workflow.
- Spend engine short-circuits `Compute` to zero for `plan_included`
  and `trial` events so flat-rate traffic doesn't inflate metered
  `cost_usd`.

## 0.4.0 - 2026-05-12

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

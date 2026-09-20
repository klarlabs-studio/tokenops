# ADR 0004 — TokenOps is a control plane, not a rate-limit predictor

- **Status:** Accepted
- **Date:** 2026-09-20
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0003 (authoritative cost), `docs/architecture-ddd.md`,
  `internal/archlint/archlint_test.go`, `README.md`

## Context

Rate-limit prediction is how TokenOps entered the market and it still works.
It is no longer what the product is. The binary today observes seven vendor
sources, aggregates spend, grades agent DX, coaches prompts, guards redundant
reads, routes models, compresses command output and serves a dashboard — and
the README still promises one sentence about subscription windows.

The mismatch is not a marketing problem. It has shaped the architecture:
capabilities were added wherever the nearest adapter was, so the CLI and the
MCP server each grew their own copy of the same use case, and the daemon grew a
composition root that owns every subsystem's lifecycle policy inline.

## Decision

TokenOps is **a local control plane for AI coding agents**. Its purpose is one
loop:

```
OBSERVE → MEASURE → EXPLAIN → OPTIMIZE → CONTROL → VERIFY → OBSERVE
```

Capabilities organise under three concepts, which become visible in the README,
docs, CLI, MCP and package layout:

- **Observe** — usage, tokens, headroom, spend, context, prompts, tool
  activity, tasks, workflows, agent-DX.
- **Improve** — coaching, context optimization, output compression,
  repeated-work detection, recommendations, optimization evaluation.
- **Control** — routing, budgets, rules, read guards, optimization policies,
  provider selection, governance.

A capability that cannot be placed in the loop requires justification before it
enters the core product. Rate-limit prediction stays prominent as a concrete
capability; it stops defining the architecture.

### Five questions

Every significant feature must contribute to answering one of:

1. What is happening?
2. Why is it happening?
3. Does it matter?
4. What can improve it?
5. Did the improvement actually work?

Question 5 is the one TokenOps cannot currently answer for any intervention.
Closing it is the product's differentiator, not another dashboard statistic.

## Consequences

### An application capability layer becomes the only place behaviour lives

Today `internal/cli` (~12k LOC) and `internal/mcp` (~4.9k LOC) each import
domain packages directly and each implement validation, orchestration and
formatting. That is roughly ⅓ the size of the domain, and it has already
produced divergence rather than duplication alone:

- `agent dx` — CLI `--days 0` reads everything, MCP requires `all:true`; CLI
  can exclude scratch directories, MCP cannot express it; CLI warns and renders
  on an extraction error, MCP fails the call.
- `plan headroom` — MCP sorts providers (a non-determinism bug fixed there),
  CLI still ranges an unordered map.
- `status` — two unrelated implementations over two different data sources,
  with two structs both named `statusResult`.
- `routing decide` — MCP only; its store-opening logic is written three times.

`internal/cli/parity_test.go` guards command/tool *names*, which is why all of
this passed. Name parity is not capability parity.

A capability owns its request model, validation, orchestration, result model,
provenance and errors. Adapters translate protocol to capability request and
capability result to protocol. Parity becomes architectural.

**Enforcement:** archlint constrains domain → adapter/infra today and places no
constraint on adapter → domain. That is the hole the duplicated paths grew
through. A rule restricting `internal/cli` and `internal/mcp` to the capability
layer is what stops this decaying again; it lands with an exemption list that
shrinks, never grows.

### Measurement carries its provenance

A derived value must retain enough provenance for a reader to judge how far to
trust it. `unknown` never becomes `0`; `estimated` never becomes `measured`.

The pattern already exists twice and has not spread: `plans.SignalQuality`
(`{Level, Source, Caveat, UpgradePaths}`, consumed by session budget and plan
headroom only) and `modeltier.Basis` (a seven-value enum with `BasisNone` as an
explicit unknown, used nowhere outside `modeltier`). Everything else is a
bespoke bool or string — `WindowResetEstimated`, `SpendSource`, `Priced`,
`WindowKnown`, scorecard's `*Computed` flags. `MeasurementWarning` reaches 2 of
~25 MCP tools.

The known silent conversions, in priority order:

1. `analytics/aggregator.go:346,429` — an unpriced model contributes `0` via
   `if cerr != nil { continue }`. `Summarize` reports the gap as `Unpriced`;
   `AggregateBy` does not, and `AggregateBy` feeds burn rate, forecast, top
   consumers and the dashboard.
2. `proxy/server.go:139` — with no tokenizer wired, events ship with zero
   counts by documented design.
3. `optimizer/tokensavings.go:25` — falls back to `bytes/4` and returns the
   same `int64` a tokenizer would; on error returns `0`, indistinguishable from
   a measured no-saving.

### Verification becomes part of optimization

A recommendation is incomplete until its result can be measured. The four
stages live in four disconnected stores and nothing correlates them:
`OptimizationEvent` has no `recommendation_id` or `outcome_of`, routing
approvals record `proposed`/`decided` and no outcome, and
`eventschema.coaching.Decision` is documented as recording adoption while
nothing anywhere writes it.

Two existing pieces show the right shape and should be generalised rather than
replaced:

- **read guard** measures an intervention it actually performed and
  deliberately excludes observe-mode `would_block`, because "crediting it would
  report uplift the guard did not deliver".
- **fmt learn** mines compress records against `fmt recover` events as a "did
  this intervention harm the agent" signal, and explicitly never changes
  runtime behaviour on its own.

TokenOps must distinguish *recommended*, *applied*, *measured* and *verified*.

### Health means healthy observation

`/healthz` is liveness and says so deliberately. `config.CheckStaleIngestion`
already computes per-source last-seen, silent-for duration, 7d/14d severity
tiers, and a `SourceProbe` that separates "the operator did not use this
vendor" from "the reader died" — and renders all of it as a warning string.
`LastEventBySource` has two call sites; neither `tokenops vendor-usage status`
nor `tokenops_data_sources` returns a last-seen, so an operator cannot tell a
source that died three weeks ago from one that died three hours ago.

Freshness becomes data, not prose. Pollers additionally record last successful
*poll*, distinct from last event ingested, so a live poller seeing an unchanged
snapshot is distinguishable from one holding an expired credential.

### Operational simplicity is preserved

Single Go binary, SQLite, local dashboard. No mandatory cloud service, no
external database, no broker, no plugin framework. Local-first is an
architectural advantage and a product differentiator, and this ADR does not
spend it.

## Non-goals

Rewriting TokenOps. Replacing Go or SQLite. Microservices. A generic plugin
framework. Removing advanced functionality to simplify the story. Making
probabilistic inference responsible for what can be computed deterministically.
Architectural purity at the expense of shipping.

## Sequencing

Ordered by dependency and by cost of deferral, not by the numbering of the
source intent.

**Phase 0 — correctness and safety debt found while auditing.** Independent of
everything else; ships first because it is live.

- Six `/api/*` routes bypass dashboard auth: `/api/audit`,
  `/api/rules/{analyze,conflicts,compress,inject}`, `/api/domain-events` are
  registered on the outer mux, and exact-pattern precedence means the `/api/`
  wildcard behind the middleware never sees them.
- `tokenops fmt` recovery store writes full raw stdout/stderr at `0644` in a
  `0755` directory, unpruned and unredacted, for commands that may print
  credentials. The `redaction` package exists and is wired only into the OTLP
  exporter.
- Three doc comments promise env-var secret injection that no code reads.
- `Config.Redacted()` is a hand-maintained allowlist with no completeness test.

**Phase 1 — provenance.** Nothing downstream is trustworthy without it, and it
is additive. Introduce the common measurement type, apply it first to the three
silent `0` conversions, then to spend, headroom, savings and scorecard.

**Phase 2 — freshness as health.** Small, self-contained, and the measurement
already exists. Expose per-source last-seen and last-successful-poll as data
across CLI, MCP and HTTP.

**Phase 3 — capability layer.** The largest change. Migrate capability by
capability, starting with the ones that have already diverged (agent dx, plan
headroom, status, routing decide). Each migration lands with the adapter→domain
archlint rule tightened by one package.

**Phase 4 — verification loop.** Depends on Phase 1 for honest measurement and
Phase 3 for a place to put orchestration. Add correlation identity to
`OptimizationEvent`, record outcomes against approvals, generalise the read
guard and fmt-learn patterns.

**Phase 5 — event model consolidation.** Fold `domainevents` (whose structs
carry `At` and nothing else — no ID, version or source) into the canonical
envelope, add task association, and retire the feature-specific ingestion paths
that bypass it.

**Phase 6 — daemon modularity and installed-product verification.** Decompose
the 558-line `RunWithLogger` and its 13 untracked goroutines into lifecycle
modules; add the end-to-end test that runs a built binary through init →
start → ingest → query, which nothing does today.

**Phase 7 — narrative.** Rewrite the README around the loop once the loop is
real. Doing this earlier would document an intention rather than a product.

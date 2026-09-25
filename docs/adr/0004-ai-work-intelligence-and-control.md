# ADR 0004 — TokenOps is an AI-work intelligence and control system

- **Status:** Accepted — revised 2026-09-20, the same day it was first
  written, after the mission generalised from "coding agents" to "AI work".
  The first revision framed TokenOps as a local control plane for AI *coding*
  agents. That is now Horizon I, not the domain.
- **Date:** 2026-09-20
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0003 (authoritative cost), `docs/architecture-ddd.md`,
  `internal/archlint/archlint_test.go`, `README.md`

## Context

TokenOps entered the market predicting rate-limit cutoffs. It now observes
seven vendor sources, aggregates spend, grades agent DX, coaches prompts,
guards redundant reads, routes models and compresses command output — and the
README still promises one sentence about subscription windows.

The mismatch has shaped the architecture. Capabilities landed wherever the
nearest adapter was, so the CLI and MCP server each grew their own copy of the
same use case. The daemon grew a composition root that owns every subsystem's
lifecycle inline. And the domain model stopped at *tokens*, which is why
nothing in the codebase can say whether any of this work succeeded.

## Decision

**TokenOps is the operating system for AI work.** It observes how humans,
agents, models and tools perform work; understands what happened and why;
measures resources, economics, quality and outcomes; explains inefficiencies;
optimizes and safely controls execution; verifies whether interventions helped;
and learns how future work should be performed.

The loop is eight stages, not six:

```
Observe → Understand → Measure → Explain → Optimize → Control → Verify → Learn
   ↑                                                                        │
   └────────────────────────────────────────────────────────────────────────┘
```

`Understand` (telemetry → meaningful work) and `Learn` (verification →
reusable knowledge) are the two stages that turn a measurement tool into a
system that improves what it measures.

### Architectural invariant

**No core TokenOps abstraction may require AI work to mean software
development.** Repositories, pull requests, commits, files and coding sessions
are evidence and domain-specific resources, never universal primitives. They
belong in adapters and specialised domains layered on top of a domain-neutral
core.

### Core ontology

Ten concepts, semantic before syntactic: **Work**, **Actor**, **Execution**,
**Resource**, **Event**, **Measurement**, **Outcome**, **Intervention**,
**Experiment**, **Policy**.

The Go representation may evolve. What must not drift is the meaning: work is
something someone wants accomplished; an execution is one *attempt* at it, so
the same work can have several and be compared; an outcome is what the work
produced, distinct from what it consumed.

## Consequences

### Three primitives do not exist at all

Measured against the current code:

| Concept | What exists today |
|---|---|
| Work | `tasks.Task` — an operator-marked `{ID, Description, StartedAt, CompletedAt, SessionID}` in `~/.tokenops/tasks.jsonl`. No goal, constraints, parent/child, or expected outcome. `governance/story` reconstructs task boundaries from transcripts. |
| Actor | **Nothing.** There is a `Provider` string and a `SessionID`. No actor concept, so delegation and multi-agent work cannot be represented. |
| Execution | **Nothing.** No concept of an attempt. Work cannot have two executions, so nothing can be compared or experimented on. |
| Resource | Partial and good: model, tokens, cache, tools, latency all ride on events. |
| Event | `eventschema.Envelope` (ID, schema version, trace/span, source, 4 typed payloads) — plus a parallel in-process `domainevents` whose structs carry `At` and nothing else. |
| Measurement | `plans.SignalQuality` (2 consumers) and `modeltier.Basis` (1). Everything else is a bespoke bool or string. |
| **Outcome** | **Zero. `grep -r Outcome` over `internal/` and `pkg/` returns no non-test match.** |
| Intervention | `OptimizationEvent.Decision`, set at emit time and never revisited; `routingapproval`; read guard. |
| Experiment | `eval` (synthetic fixtures), `replay` (counterfactual, never writes back). No baseline-vs-intervention over real executions. |
| Policy | Budgets, rules, routing rules, read-guard config, RBAC — real, but scattered. |

**Outcome is the single largest gap between this intent and the code.** Without
it TokenOps can only optimize consumption. `Execution` is what makes
`Experiment` possible; `Actor` is what makes delegation representable. These
three are the foundation the later horizons stand on.

### Control modes exist five times, in four vocabularies

The intent asks for one ladder: observe-only → recommend → require approval →
automatic. The code already has five, none of which agree:

- `config.Mode` — `passive` | `active` (daemon-wide)
- `CoachingConfig.Delivery` — `observe` | `advise` | `intervene`
- `readguard.Mode` — `observe` | `active`
- `routingapproval` — a propose/decide gate, expressed as neither
- `SmartRoutingConfig.Intervention` — `off` | `advise` | `delegate` | `auto`

*Corrected while implementing Phase 5:* this audit counted four. The fifth,
smart routing's per-turn guard, was found while wiring the ladder. It is the
only one whose default is not the bottom rung — the config documents an empty
value as `advise` — so a machine that enabled smart routing without naming an
intervention already has a subsystem speaking unprompted. Its `delegate` rung
is also the only other expression of "requires a human decision" outside
`routingapproval`, in different words.

Control authority is currently a per-subsystem opinion. It becomes one policy
concept in the control layer, and each subsystem expresses its authority in
those terms.

### Capabilities become interface-independent

`internal/cli` (~12k LOC) and `internal/mcp` (~4.9k LOC) each import domain
packages directly and each implement validation, orchestration and formatting —
roughly ⅓ the size of the domain. This has produced divergence, not just
duplication:

- `agent dx` — CLI `--days 0` reads everything, MCP requires `all:true`; CLI
  can exclude scratch directories, MCP cannot express it; CLI warns and renders
  on an extraction error, MCP fails the call.
- `plan headroom` — MCP sorts providers (a non-determinism bug fixed there),
  CLI still ranges an unordered map.
- `status` — two unrelated implementations, two structs both named
  `statusResult`.
- `routing decide` — MCP only; its store-opening logic is written three times.

`internal/cli/parity_test.go` guards command/tool *names*, which is why all of
this passed. Name parity is not capability parity.

**Enforcement:** archlint constrains domain → adapter/infra and places no
constraint on adapter → domain. That is the hole these paths grew through. A
rule restricting adapters to the capability layer is what stops this decaying
again, landing with an exemption list that shrinks and never grows.

### Measurement carries its provenance

`unknown` never becomes `0`; `estimated` never becomes `measured`. The pattern
exists twice and has not spread. The known silent conversions, in priority
order:

1. `analytics/aggregator.go:346,429` — an unpriced model contributes `0` via
   `if cerr != nil { continue }`. `Summarize` reports the gap as `Unpriced`;
   `AggregateBy` does not, and `AggregateBy` feeds burn rate, forecast, top
   consumers and the CLI/MCP surfaces.
2. `proxy/server.go:139` — with no tokenizer wired, events ship with zero
   counts by documented design.
3. `optimizer/tokensavings.go:25` — falls back to `bytes/4` and returns the
   same `int64` a tokenizer would; on error returns `0`, indistinguishable from
   a measured no-saving.

### Verification replaces assumed savings

`tokens removed × nominal price` is never presented as proven savings. An
intervention may cut tokens while raising cost, breaking caching, causing
retries, adding latency or degrading the outcome. TokenOps must be able to
discover those effects, which requires Execution and Outcome to exist first.

Today the four stages live in four disconnected stores and nothing correlates
them: `OptimizationEvent` has no `recommendation_id` or `outcome_of`, routing
approvals record `proposed`/`decided` and no result, and
`eventschema.coaching.Decision` is documented as recording adoption while
nothing anywhere writes it.

Two existing pieces have the right shape and should be generalised:

- **read guard** measures an intervention it actually performed and
  deliberately excludes observe-mode `would_block`, because "crediting it would
  report uplift the guard did not deliver".
- **fmt learn** mines compress records against `fmt recover` events as a "did
  this intervention harm the agent" signal, and never changes runtime behaviour
  on its own.

### Health means useful observation

`/healthz` is liveness and says so deliberately. `config.CheckStaleIngestion`
already computes per-source last-seen, silent-for duration, 7d/14d severity
tiers, and a `SourceProbe` separating "the operator did not use this vendor"
from "the reader died" — and renders all of it as a warning string.
`LastEventBySource` has two call sites; neither `tokenops vendor-usage status`
nor `tokenops_data_sources` returns a last-seen. Freshness becomes data.
Pollers additionally record last successful *poll*, so a live poller seeing an
unchanged snapshot is distinguishable from one holding an expired credential.
Health eventually covers the control path too, not only ingestion.

### TokenOps owns intelligence, not infrastructure

It does not become Grafana, Prometheus, Datadog, ClickHouse, OpenTelemetry, a
warehouse or a BI tool. It consumes from and exposes to them — the OTLP
exporter is the first instance and the model for the rest. Interoperability is
a feature: a Grafana team should visualise TokenOps measurements, an agent
should query it over MCP, a workflow should call it over HTTP.

### Operational simplicity is preserved

Single Go binary, SQLite, local API. No mandatory cloud, external
database, broker or plugin framework. Local-first buys privacy, low friction,
ownership and resilience, and this ADR does not spend it.

## Horizons

**I — Coding agents.** Become exceptional here first: usage, economics, tasks,
context, tools, DX, optimization, outcomes across Claude Code, Codex, Cursor,
Gemini, OpenCode, Copilot.

**II — AI agents.** Generalise the same intelligence: evaluation, routing,
governance, experimentation, control over agent systems.

**III — AI work.** Understand AI-mediated work regardless of implementation.

Depth before breadth. The *architecture* generalises immediately; the *product*
generalises when the underlying intelligence is ready.

## Sequencing

Ordered by dependency and cost of deferral, not by the intent's numbering.

**Phase 0 — safety debt.** Independent of everything else; it was live.
*Complete as of 0.70.0.*

- `tokenops fmt`'s recovery store wrote full raw stdout/stderr at `0644` in a
  `0755` directory, unpruned and unredacted, for commands that may print
  credentials. The `redaction` package existed and was wired only into OTLP.
  Now `0600`/`0700`, redacted through the pattern rules, pruned at 14 days.
- `domain-events.jsonl` was written at `0644`. Now `0600`, on creation and
  after rotation.

  *Corrected while implementing:* this item also claimed the log "grows
  unbounded while a rotating one exists". That was wrong. `NewJSONLog`
  already rotates at 10 MiB keeping 3 backups, and it is the only
  constructor the daemon calls. Only the mode was a real finding.
- One doc comment promised an `env:<NAME>` credential source no code produced
  or read, and `SECURITY.md` advised "environment substitution" that
  `config.Load` did not implement.

  *Corrected while implementing:* the audit counted three such comments. Two
  of them — `SetupDeps.Getenv` and `envSecret` — describe behaviour that does
  exist. Rather than retract the `SECURITY.md` promise, the environment path
  is now real: `config.CredentialEnvVars` supplies all five credentials at
  load time, they win over the file, and nothing writes them back.
- `Config.Redacted()` was a hand-maintained allowlist with no completeness
  test. A reflection test now fails on any credential-shaped field it misses;
  the allowlist was in fact complete, and is now held that way.
- `WriteFile` did not tighten perms on a pre-existing config. Both the file
  and its directory are now repaired on every write, so an upgrade fixes the
  installation rather than only new machines.

**Phase 1 — provenance.** Nothing downstream is trustworthy without it, and it
is additive. The common measurement type, applied first to the three silent
`0` conversions, then to spend, headroom, savings and scorecard.

**Phase 2 — freshness as health.** Small, self-contained, already measured.
Expose per-source last-seen and last-successful-poll as data.

**Phase 3 — the missing primitives.** Introduce Work, Actor, Execution and
Outcome as domain-neutral concepts, with the existing `tasks` and `story`
reconstruction as their first adapter. This is the phase that changes what
TokenOps *is*; everything after depends on it.

**Phase 4 — capability layer.** Migrate capability by capability, starting with
those that have already diverged. Each migration tightens the adapter→domain
archlint rule by one package.

**Phase 5 — verification and experiment.** Correlation identity on
interventions, outcomes recorded against them, baseline-vs-intervention
comparison over real executions. Generalise the read-guard and fmt-learn
patterns. Unify the four control ladders into one policy concept.

**Phase 6 — event model consolidation.** Fold `domainevents` into the canonical
envelope; add work, execution and actor association; retire the
feature-specific ingestion paths that bypass it.

**Phase 7 — runtime modularity and installed-product verification.** Decompose
the 558-line `RunWithLogger` and its 13 untracked goroutines into lifecycle
modules; add the end-to-end test that runs a built binary through init → start
→ ingest → reconstruct → query, which nothing does today.

**Phase 8 — intent-oriented MCP and the narrative.** Compose the granular tools
behind intent operations; rewrite the README around the loop once the loop is
real. Doing either earlier would document an intention rather than a product.

**Phase 9 — surface-native insight.** Represent evidence-backed insights
structurally before rendering, then apply surface-specific presentation
policies. Begin with concise readiness/attention summaries shared by MCP and
CLI; add a resource glance that composes measured session budget and plan
headroom without inventing thresholds; add a bounded workflow insight from
reconstructed traces and existing waste-detector findings. Work insight must
not infer task success or overall quality from the absence of a finding.

## Non-goals

Rewriting TokenOps. Replacing Go or SQLite. Microservices. A generic plugin
framework. Becoming a generic observability backend or BI product. Cloud
dependencies without concrete benefit. Optimizing tokens at the expense of
outcomes. Automating control before it can explain its decisions. Sacrificing
measurement integrity for impressive metrics. Generalising beyond coding by
weakening the core product.

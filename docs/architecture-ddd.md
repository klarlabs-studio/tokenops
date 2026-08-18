# TokenOps DDD Architecture

This document describes the bounded contexts, ubiquitous language, and
layering rules that govern the TokenOps codebase. Every Go package
belongs to exactly one context and obeys the layering constraints below.
PRs that cross these boundaries must update this document.

## Layering

```
┌────────────────────────────────────────────────────────────┐
│ adapters (CLI, MCP, HTTP, dashboard)                       │
│   internal/cli, internal/mcp, internal/proxy/*_api.go,     │
│   web/dashboard                                             │
│                                                            │
│   - parse user/protocol input                              │
│   - format output                                          │
│   - delegate to application services                       │
└──────────────────────────┬─────────────────────────────────┘
                           │
                           ▼
┌────────────────────────────────────────────────────────────┐
│ application services (per bounded context)                 │
│   rules.LoadCorpus, rules.RunBenchSpec                     │
│   eval.Run, eval.PersistBaseline                           │
│   scorecard.Build / BuildFromStore                         │
│   coverdebt.Analyze                                        │
│   forecast.AutoForecast                                    │
│   replay.DefaultPipeline + Engine.Replay                   │
│                                                            │
│   - orchestrate domain services                            │
│   - one entry point per use case                           │
└──────────────────────────┬─────────────────────────────────┘
                           │
                           ▼
┌────────────────────────────────────────────────────────────┐
│ domain (entities, value objects, domain services)          │
│   rules: RuleDocument (aggregate), RuleBlock, Analyzer,    │
│          Compressor, Router, ROIEngine, Benchmark          │
│   eval:  Suite (aggregate), Case, Runner, Gate             │
│   scorecard: Scorecard (value), LiveKPIs, Compute          │
│   coverdebt: PackagePolicy, Coverage, Report               │
│   spend: Engine, Table                                     │
│   workflow: Trace                                          │
│   redaction: Redactor                                      │
│   forecast: Holt, Linear                                   │
│   pkg/eventschema: PromptEvent, WorkflowEvent, ...         │
│                                                            │
│   - pure, framework-free                                   │
│   - factories enforce invariants (NewRuleDocument,         │
│     NewSuite, NewCase)                                     │
└──────────────────────────┬─────────────────────────────────┘
                           │
                           ▼
┌────────────────────────────────────────────────────────────┐
│ infrastructure (ports + concrete adapters)                 │
│   internal/storage/sqlite       (event store adapter)      │
│   internal/otlp                 (telemetry export adapter) │
│   internal/events               (telemetry bus)            │
│   internal/domainevents         (in-process domain bus)    │
│   internal/proxy                (HTTP server)              │
│   internal/contexts/prompts/tokenizer            (provider tokenizer impl)  │
│   internal/contexts/rules.Ingestor       (filesystem adapter        │
│                                  for rule corpus)          │
│   internal/bootstrap            (composition root)         │
│                                                            │
│   - implements ports defined by domain                     │
│   - never imported by domain code                          │
└────────────────────────────────────────────────────────────┘
```

**Allowed import direction**: adapters → application → domain → ports.
Infrastructure imports application/domain interfaces only; nothing in
domain may import an infrastructure package by concrete type. Documented
sqlite exemptions (enforced by `internal/archlint` `storageExempt`):

- `scorecard/service.go` adapts `*sqlite.Store` to the `EventReader` port.
- `observability/analytics`, `security/audit`, `workflows/workflow`,
  `optimization/replay`, `telemetry/retention`, `tasks` take `*sqlite.Store`
  as an isolated adapter. New sqlite users must be added to `storageExempt`
  *and* actually import sqlite (`TestStorageExemptImportsSQLite`).

## Bounded Contexts

| Context          | Package(s)                                 | Aggregate Root     | Ubiquitous Terms                              |
|------------------|--------------------------------------------|--------------------|-----------------------------------------------|
| Prompts          | `pkg/eventschema`, `internal/contexts/prompts/{tokenizer,providers,llm}` | `PromptEvent` | provider, model, prompt, tokens, hash |
| Workflows        | `internal/contexts/workflows/workflow` | `WorkflowEvent` | workflow, step, agent, cumulative tokens |
| Optimization     | `internal/contexts/optimization/optimizer/*`, `eval`, `formatter`, `fmtlearn`, `replay` | `OptimizationEvent` | optimizer kind, formatter, loss level, critical line |
| Coaching         | `internal/contexts/coaching/{coaching,efficiency,waste,prompts,replies,tools}` | `CoachingEvent` | recommendation kind, efficiency score |
| Rule Intelligence| `internal/contexts/rules` | `RuleDocument` | rule source, section, scope, ROI score |
| Spend            | `internal/contexts/spend/{spend,forecast,plans,pricing,session,vendorusage/*}` | `Engine` (svc) | cost, pricing table, plan, poller |
| Observability    | `internal/contexts/observability/{analytics,anomaly,observ}` | (svc) | bucket, group, row, summary, anomaly |
| Governance       | `internal/contexts/governance/{scorecard,coverdebt,budget}` | `Scorecard` | KPI, gate, risk score, coverage goal |
| Security         | `internal/contexts/security/{redaction,dashauth,audit,rbac,tlsmint}` | `Redactor` | finding, placeholder, secret, entropy |
| Tasks            | `internal/contexts/tasks` | `Task` | operator-marked window, metrics |
| Telemetry        | `internal/events`, `internal/otlp`, `internal/storage/sqlite`, `internal/contexts/telemetry/retention` | (svc) | envelope, sink, schema version, prune |

## Ubiquitous Language

| Term              | Definition                                                                                          |
|-------------------|-----------------------------------------------------------------------------------------------------|
| envelope          | The common header carried by every TokenOps event (id, type, timestamp, payload).                    |
| prompt            | A single LLM request/response cycle observed by the proxy.                                           |
| workflow          | A multi-step agent run identified by `WorkflowID`. Spans many prompts.                               |
| session           | A user-attributed sequence of prompts identified by `SessionID`. May span workflows.                 |
| agent             | The orchestrating component that drives a workflow. Identified by `AgentID`.                         |
| rule artifact     | A single operational rule document (CLAUDE.md, AGENTS.md, .cursor/rules/*, *.mcp.yaml).              |
| rule section      | An addressable block within a rule artifact, keyed by heading path.                                   |
| ROI score         | `(TokensSaved − ContextTokens) / ContextTokens` for a rule over a measurement window.                |
| FVT               | First-Value Time. Median first-prompt latency per session.                                            |
| TEU               | Token Efficiency Uplift. `sum(EstimatedSavings) / sum(InputTokens)`.                                  |
| SAC               | Spend Attribution Completeness. % of PromptEvents carrying any attribution signal.                   |
| gate              | A regression check that compares a current report against a baseline and emits violations.            |
| optimizer pipeline| The ordered set of optimizers (prompt_compress, command_fmt, dedupe, retrieval_prune, context_trim) applied to a request. |
| formatter         | A deterministic command-output compressor for one command (built-in or user config), guaranteeing critical-line survival at every loss level. |
| loss level        | How aggressively a formatter strips noise: conservative, balanced, or aggressive. Critical lines survive at all levels. |
| critical line     | An output line a formatter must never drop (error, failure, changed state); enforced by the engine, not the individual formatter. |

## Aggregate Factories

External code must use these factories instead of struct literals:

- `rules.NewRuleDocument(sourceID, path, repoID, body, source, scope)` — validates required fields, defaults scope, parses blocks.
- `eval.NewCase(...)` — validates ID, provider, body, at-least-one-expectation.
- `eval.NewSuite(name, description, cases)` — validates name; `Suite.AddCase` is the only sanctioned post-construction mutation.

## Domain Events

`internal/domainevents` carries cross-context coordination events,
distinct from `internal/events` which carries telemetry envelopes.
Subscribers register via `Bus.Subscribe(kind, handler)`. Canonical kinds
live in `internal/domainevents/events.go`.

## Composition Root

`internal/bootstrap.New(ctx, opts)` builds the shared core (store, spend
engine, tokenizer registry, redactor, domain bus). It is not the only
composition root:

- `daemon.RunWithLogger` (`internal/daemon`) wires pollers, OTLP, dashauth,
  analytics HTTP, mDNS, retention, and the URL hint. This is what
  `tokenops start` / `tokenopsd` run.
- `tokenops serve` calls `bootstrap.New` independently. The two processes
  share `events.db` and `daemon.url` — serve does not start pollers.

Adapters receive `*bootstrap.Components` rather than constructing their
own `spend.Engine` or `sqlite.Store`.

## Adapter package layout

`internal/proxy` is a single adapter package holding the HTTP server,
provider routes, and the analytics / rules / events handler families.
Splitting into sub-packages is unnecessary today because:

- the layering rule (no domain → adapter import) is enforced by
  `internal/archlint`, not by directory; and
- all handler files (`api.go`, `rules_api.go`, `events_api.go`) share
  a small set of helpers (`writeAPIJSON`, `writeAPIError`,
  `*Server` private fields) that would have to be re-exported or
  duplicated under a split.

If the package grows past ~3000 LOC it should be split into
`proxy/server`, `proxy/analytics`, `proxy/rules`, `proxy/events`.

## Anti-Corruption

Cross-context translation happens only at the adapter boundary:

- HTTP/MCP/CLI translate protocol payloads into application service
  inputs (e.g., `rules.BenchSpec` is the published wire form; the
  domain consumes only materialised `rules.Profile` / `rules.Scenario`).
- `scorecard.sqliteReader` translates `*sqlite.Store` queries into the
  `EventReader` port the domain understands.
- `redaction.Redactor` mediates between raw user content and any
  outbound envelope, never the other direction.

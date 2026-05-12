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
domain may import an infrastructure package by concrete type. The two
documented exceptions today (kept under review):

- `scorecard/service.go` adapts `*sqlite.Store` to the `EventReader`
  port in a single file, isolated from `Compute`.
- `rules/ingest.go` Ingestor adapter holds `os` + `io/fs` imports; no
  application service references those packages directly.

## Bounded Contexts

| Context          | Package(s)                                 | Aggregate Root     | Ubiquitous Terms                              |
|------------------|--------------------------------------------|--------------------|-----------------------------------------------|
| Prompts          | `pkg/eventschema`, `internal/contexts/prompts/tokenizer`    | `PromptEvent`      | provider, model, prompt, tokens, hash         |
| Workflows        | `internal/contexts/workflows/workflow`                        | `WorkflowEvent`    | workflow, step, agent, cumulative tokens      |
| Optimization     | `internal/contexts/optimization/optimizer/*`, `internal/contexts/optimization/eval`    | `OptimizationEvent`| optimizer kind, decision, quality score       |
| Coaching         | `internal/contexts/coaching/coaching`, `internal/contexts/coaching/efficiency` | `CoachingEvent`    | recommendation kind, efficiency score         |
| Rule Intelligence| `internal/contexts/rules`                           | `RuleDocument`     | rule source, section, scope, ROI score        |
| Spend            | `internal/contexts/spend/spend`, `internal/contexts/spend/forecast`      | `Engine` (svc)     | cost, pricing table, currency, burn rate      |
| Observability    | `internal/contexts/observability/analytics`, `internal/contexts/observability/anomaly`   | (svc)              | bucket, group, row, summary, anomaly          |
| Governance       | `internal/contexts/governance/scorecard`, `internal/contexts/governance/coverdebt` | `Scorecard`        | KPI, gate, risk score, coverage goal          |
| Security         | `internal/contexts/security/redaction`, `internal/contexts/security/dashauth`  | `Redactor`         | finding, placeholder, secret, entropy         |
| Replay           | `internal/contexts/optimization/replay`                          | `Engine` (svc)     | session selector, step diff, pipeline         |
| Telemetry        | `internal/events`, `internal/otlp`, `internal/storage/sqlite` | (svc) | envelope, sink, schema version |

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
| optimizer pipeline| The ordered set of optimizers (prompt_compress, dedupe, retrieval_prune, context_trim) applied to a request. |

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

`internal/bootstrap.New(ctx, opts)` is the single composition root.
Every adapter receives `*bootstrap.Components` rather than constructing
its own `spend.Engine`, `tokenizer.Registry`, or `sqlite.Store`. The
daemon entry point wires bootstrap once at startup.

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

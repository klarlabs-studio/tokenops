# Telemetry Contracts and Lineage

This document defines the data contracts for every telemetry field
emitted by TokenOps. Each contract specifies origin, type constraints,
guarantees, and a responsible owner. Compatibility tests in
`internal/events/contract_test.go` verify that the Go types, SQLite
schema, OTLP attributes, and Protobuf definitions are consistent.

## Schema Change Policy

Changes to telemetry fields follow a compatibility matrix:

| Change | Level | Bump | Action Required |
|---|---|---|---|
| Add an optional field | Additive | Patch | Update Go struct, SQLite, OTLP mapping, Protobuf, docs, test |
| Add a required field | Backward-incompatible | Major | Migration policy; announce deprecation cycle |
| Rename a field | Backward-incompatible | Major | Dual-write old+new for one minor cycle |
| Remove a field | Backward-incompatible | Major | Deprecate first, remove in next major |
| Change field type (widen) | Potentially incompatible | Minor | Verify SQLite/OTLP consumers accept new range |
| Change field type (narrow) | Breaking | Major | Migration policy |
| Add an enum member | Additive | Minor | Update Go enum, OTLP mapping, Protobuf, docs, test |
| Remove an enum member | Breaking | Major | Deprecate first, remove in next major |

The `SchemaVersion` constant in `pkg/eventschema/version.go` is bumped
accordingly. All changes must update `docs/telemetry-contracts.md` and
pass `make verify`.

## Contract Owners

| Contract | Owner | Review Required |
|---|---|---|
| Event Envelope (`Envelope`) | @tokenops/proxy | Any schema change |
| Prompt Event (`PromptEvent`) | @tokenops/proxy | Any schema change |
| Workflow Event (`WorkflowEvent`) | @tokenops/sdk | Any schema change |
| Optimization Event (`OptimizationEvent`) | @tokenops/optimizer | Any schema change |
| Coaching Event (`CoachingEvent`) | @tokenops/optimizer | Any schema change |
| Rule Source Event (`RuleSourceEvent`) | @tokenops/rules | Any schema change |
| Rule Analysis Event (`RuleAnalysisEvent`) | @tokenops/rules | Any schema change |
| SQLite schema | @tokenops/storage | Migration version bump |
| OTLP attribute mapping | @tokenops/observability | Attribute key change |
| Protobuf definitions | @tokenops/core | Any field/add/remove/rename |

---

## Lineage

### Proxy-to-Storage Flow

```
Client Request
  │
  ├─ proxy/observation.go: capture body, hash (SHA-256),
  │   tokenize (PreflightCount), extract attribution headers
  │   (X-Tokenops-Workflow-Id, X-Tokenops-Agent-Id,
  │    X-Tokenops-Session-Id, X-Tokenops-User-Id)
  │
  ├─ proxy/cache_middleware.go: [optional] serve from cache,
  │   emit synthetic PromptEvent with CacheHit=true
  │
  ├─ ReverseProxy → upstream provider
  │    └─ meteredBody: measure TTFT, count output tokens
  │        └─ Done() → build PromptEvent Envelope
  │
  └─ events/bus.go: AsyncBus → batch → MultiSink
       │
       ├─ storage/sqlite/store.go: AppendBatch → INSERT
       │   (indexed columns extracted, full payload stored as JSON)
       │
       └─ otlp/exporter.go: envelopeToLogRecord → POST /v1/logs
           (redacted via redaction.RedactEnvelope)
```

### Rule Intelligence Flow

```
Rule corpus on disk (CLAUDE.md, AGENTS.md, .cursor/rules/**, *.mcp.{yaml,yml,json})
  │
  ├─ rules/ingest.go: Discover (glob) → LoadPath (read + size cap)
  │   └─ ParseMarkdown: ATX heading split, code-fence aware,
  │       anchors as slash-joined heading path
  │
  ├─ rules/analyzer.go: per-provider tokenize → DocumentSummary
  │   ├─ ToSourceEvent: RuleSourceEvent envelope
  │   └─ DuplicateGroups: tokenizer-independent hash buckets
  │
  ├─ rules/conflicts.go: DetectConflicts → Finding{redundant|drift|anti_pattern}
  │   └─ Finding.AsAnalysisEvent: RuleAnalysisEvent (ConflictsWith/RedundantWith)
  │
  ├─ rules/compress.go: Compressor.Compress → CompressionResult
  │   └─ AsAnalysisEvent: RuleAnalysisEvent (CompressedTokens)
  │
  ├─ rules/roi.go: ROIEngine.Analyze(Exposure) → RuleAnalysisEvent
  │   (ROIScore, TokensSaved, RetriesAvoided, ContextReduction)
  │
  └─ rules/router.go: Router.Select(SelectionSignals) → SelectionResult
       └─ AsAnalysisEvents: RuleAnalysisEvent per selected section

Consumers:
  - CLI (tokenops rules analyze|conflicts|compress|inject|bench)
  - HTTP API (/api/rules/{analyze,conflicts,compress,inject}) → Vue dashboard
  - MCP tools (tokenops_rules_{analyze,conflicts,compress,inject})
  - OTLP exporter (redaction-aware, tokenops.rule.* attributes)
  - SQLite store (rule_source, rule_analysis payload kinds)
```

### Field-Level Lineage

| Field | Origin | Transforms | Consumers |
|---|---|---|---|
| `Envelope.ID` | UUIDv7 generated in proxy | None | Storage (PK), OTLP (attribute) |
| `Envelope.Timestamp` | `time.Now().UTC()` at proxy | Nanosecond precision | Storage (indexed), OTLP (timeUnixNano) |
| `Envelope.TraceID` | W3C traceparent header | Pass-through | Storage, OTLP |
| `PromptEvent.Provider` | Route config mapping | Normalized to enum | Storage (indexed), OTLP (gen_ai.system), Analytics (group) |
| `PromptEvent.RequestModel` | `provider.Normalize(body)` | Canonicalized | Storage (indexed), OTLP (gen_ai.request.model), Spend engine |
| `PromptEvent.InputTokens` | `tokenizer.PreflightCount` | Tokenized | Storage (indexed), OTLP, Spend engine |
| `PromptEvent.OutputTokens` | `tokenizer.CountText(response)` | Tokenized | Storage (indexed), OTLP, Spend engine |
| `PromptEvent.CostUSD` | `spend.Engine.Compute(prompt)` | Post-hoc or on emit | Storage (indexed), OTLP, Analytics |
| `PromptEvent.WorkflowID` | `X-Tokenops-Workflow-Id` header | Pass-through | Storage (indexed), Analytics (group) |
| `PromptEvent.AgentID` | `X-Tokenops-Agent-Id` header | Pass-through | Storage (indexed), Analytics (group) |
| `PromptEvent.CacheHit` | Cache middleware synthetic | Boolean flag | Storage, OTLP |
| `WorkflowEvent.WorkflowID` | SDK/CLI parameter | Pass-through | Storage (indexed), Workflow reconstruction |
| `OptimizationEvent.Kind` | Optimizer `Kind()` method | Enum value | Storage, OTLP, Dashboard |
| `OptimizationEvent.Decision` | Pipeline `decide()` | Enum value | Storage, OTLP, Dashboard |
| `CoachingEvent.EfficiencyScore` | Efficiency engine | Computed 0.0–1.0 | Storage, Dashboard |
| `RuleSourceEvent.SourceID` | `rules.MakeSourceID(repoID, path)` | Stable across snapshots | Storage, OTLP (`tokenops.rule.source_id`), Dashboard |
| `RuleSourceEvent.Source` | `rules.ClassifySource(path)` | Enum value | Storage, OTLP, Dashboard |
| `RuleSourceEvent.TotalTokens` | `tokenizer.CountText(body)` | Tokenized | Storage, OTLP, Dashboard leaderboard |
| `RuleSourceEvent.Sections[i].Hash` | `sha256(body)` | SHA-256 hex | Storage, Compressor, Conflict detector |
| `RuleAnalysisEvent.ROIScore` | `rules.ROIEngine.score(Exposure)` | Normalized economic ratio | Storage, OTLP (`tokenops.rule.roi_score`), Dashboard |
| `RuleAnalysisEvent.ConflictsWith` | `rules.DetectConflicts` Finding | List of SectionIDs | Storage, OTLP (`tokenops.rule.conflicts_with`) |
| `RuleAnalysisEvent.CompressedTokens` | `rules.Compressor.Compress` | Post-distillation token count | Storage, OTLP, Dashboard |
| `Envelope.Payload` | Event-specific type | JSON serialized | Storage (payload column) |

---

## 1. Envelope Contract

**File:** `pkg/eventschema/event.go`
**Protobuf:** `pkg/eventschema/proto/v1/events.proto` (message Envelope)
**SQLite:** `events` table (all columns)
**OTLP:** `internal/otlp/exporter.go` (common attributes)

| Field | Type | Required | SQLite Column | OTLP Key | Constraints |
|---|---|---|---|---|---|
| `ID` | string | yes | `id` (PK) | `tokenops.event.id` | UUIDv7 format; max 64 chars |
| `SchemaVersion` | string | yes | `schema_version` | `tokenops.schema_version` | Semver; current `"1.1.0"` |
| `Type` | EventType | yes | `type` | `tokenops.event.type` | One of: prompt, workflow, optimization, coaching, rule_source, rule_analysis |
| `Timestamp` | time.Time | yes | `timestamp_ns` | (timeUnixNano) | UTC; nanosecond precision |
| `TraceID` | *string | no | `trace_id` | `trace_id` | 32-char hex W3C format |
| `SpanID` | *string | no | `span_id` | `span_id` | 16-char hex W3C format |
| `Source` | *string | no | `source` | `tokenops.source` | Max 64 chars |
| `Attributes` | map[string]string | no | `attributes` | (pass-through) | Max 100 entries; keys max 128 chars |

### Invariants
- `ID` is globally unique and monotonically ordered (UUIDv7).
- `Type` must match the concrete type of `Payload`.
- `Timestamp` must be within ±5s of the proxy's system clock.

---

## 2. PromptEvent Contract

**File:** `pkg/eventschema/prompt.go`
**SQLite columns:** `provider, model, input_tokens, output_tokens, total_tokens, cost_usd, workflow_id, agent_id, session_id, user_id`
**OTLP:** Full attribute mapping in `pkg/eventschema/otel.go`

| Field | Type | Required | SQLite | OTLP | Constraints |
|---|---|---|---|---|---|
| `PromptHash` | string | yes | (payload) | `tokenops.prompt.hash` | SHA-256 hex; 64 chars |
| `Provider` | Provider | yes | `provider` | `gen_ai.system` | One of: openai, anthropic, gemini |
| `RequestModel` | string | yes | `model` | `gen_ai.request.model` | Max 128 chars |
| `ResponseModel` | *string | no | — | `gen_ai.response.model` | Max 128 chars |
| `InputTokens` | int64 | yes | `input_tokens` | `gen_ai.usage.input_tokens` | ≥0 |
| `OutputTokens` | int64 | yes | `output_tokens` | `gen_ai.usage.output_tokens` | ≥0 |
| `TotalTokens` | int64 | yes | `total_tokens` | `gen_ai.usage.total_tokens` | InputTokens + OutputTokens |
| `CachedInputTokens` | *int64 | no | — | `tokenops.usage.cached_input_tokens` | ≥0; ≤InputTokens |
| `ContextSize` | int64 | yes | — | `tokenops.prompt.context_size` | ≥0 |
| `CostUSD` | *float64 | no | `cost_usd` | `tokenops.cost_usd` | ≥0 when set |
| `WorkflowID` | *string | no | `workflow_id` | `tokenops.workflow.id` | From header or empty |
| `Latency` | time.Duration | yes | — | `tokenops.latency_ns` | ≥0 |

### Invariants
- `TotalTokens == InputTokens + OutputTokens` always.
- `CostUSD` is computed by `spend.Engine` using `(provider, model, InputTokens, OutputTokens)`.
- `PromptHash` is deterministic: same canonical body → same hash.

---

## 3. WorkflowEvent Contract

**File:** `pkg/eventschema/workflow.go`
**SQLite columns:** `workflow_id, agent_id, input_tokens, output_tokens, total_tokens, cost_usd`

| Field | Type | Required | SQLite | OTLP | Constraints |
|---|---|---|---|---|---|
| `WorkflowID` | string | yes | `workflow_id` | `tokenops.workflow.id` | Max 256 chars |
| `AgentID` | *string | no | `agent_id` | `tokenops.agent.id` | Max 256 chars |
| `State` | WorkflowState | yes | — | `tokenops.workflow.state` | One of: started, progress, completed, failed |
| `StepCount` | int64 | yes | — | `tokenops.workflow.step_count` | ≥0; monotonic within a workflow |
| `CumulativeTotalTokens` | int64 | yes | `total_tokens` | — | ≥0; monotonic |
| `CumulativeCostUSD` | *float64 | no | `cost_usd` | — | ≥0 when set |
| `Duration` | *time.Duration | no | — | — | ≥0 |

### Invariants
- `StepCount` must be monotonic within `(WorkflowID, AgentID)`.
- `CumulativeCostUSD` can only increase; reset only on workflow restart.

---

## 4. OptimizationEvent Contract

**File:** `pkg/eventschema/optimization.go`
**SQLite columns:** `workflow_id, agent_id`

| Field | Type | Required | SQLite | OTLP | Constraints |
|---|---|---|---|---|---|
| `PromptHash` | string | yes | — | `tokenops.prompt.hash` | Links to PromptEvent |
| `Kind` | OptimizationType | yes | — | `tokenops.optimization.type` | Must match an optimizer `Kind()` |
| `Mode` | OptimizationMode | yes | — | `tokenops.optimization.mode` | One of: passive, interactive, replay |
| `Decision` | OptimizationDecision | yes | — | `tokenops.optimization.decision` | One of: applied, accepted, rejected, skipped |
| `EstimatedSavingsTokens` | int64 | yes | — | `tokenops.optimization.estimated_savings_tokens` | ≥0 |
| `QualityScore` | *float64 | no | — | `tokenops.optimization.quality_score` | [0.0, 1.0] |

### Invariants
- `EstimatedSavingsTokens` is an estimate, not a guarantee — actual savings
  are measured by comparing PromptEvents before and after optimization.
- `QualityScore` is a heuristic; ground-truth evaluation uses the eval
  framework (`internal/eval/`).

---

## 5. CoachingEvent Contract

**File:** `pkg/eventschema/coaching.go`
**SQLite columns:** `workflow_id, agent_id, session_id`

| Field | Type | Required | SQLite | OTLP | Constraints |
|---|---|---|---|---|---|
| `SessionID` | string | yes | `session_id` | — | References replayed session |
| `Kind` | CoachingRecommendationKind | yes | — | `tokenops.coaching.kind` | One of defined enum values |
| `Summary` | string | yes | — | `tokenops.coaching.summary` | Max 1024 chars |
| `EfficiencyScore` | *float64 | no | — | — | [0.0, 1.0] |
| `EfficiencyDelta` | *float64 | no | — | — | May be negative |

### Invariants
- `EfficiencyScore` is computed from the efficiency package and may be
  nil before the first baseline evaluation.
- `Decision` defaults to `skipped` until the user acts on the coaching
  recommendation.

---

## 6. RuleSourceEvent Contract

**File:** `pkg/eventschema/rule.go`
**SQLite columns:** (payload only; not indexed)
**OTLP:** `tokenops.rule.*` keys in `pkg/eventschema/otel.go`

| Field | Type | Required | SQLite | OTLP | Constraints |
|---|---|---|---|---|---|
| `SourceID` | string | yes | (payload) | `tokenops.rule.source_id` | `MakeSourceID(repoID, path)` |
| `Source` | RuleSource | yes | (payload) | `tokenops.rule.source` | One of: claude_md, agents_md, cursor_rules, mcp_policy, repo_convention, custom |
| `Scope` | RuleScope | no | (payload) | `tokenops.rule.scope` | One of: global, repo, workflow, tool, file_glob, conditional |
| `Path` | *string | no | (payload) | `tokenops.rule.path` | Forward-slash repo-relative; redacted by `redaction.RedactEnvelope` |
| `RepoID` | *string | no | (payload) | `tokenops.rule.repo_id` | Opaque identifier; allows cross-repo aggregation |
| `Tokenizer` | *string | no | (payload) | `tokenops.rule.tokenizer` | Label e.g. `openai/cl100k_base` |
| `Provider` | Provider | no | (payload) | `gen_ai.system` | Tokenizer's provider |
| `TotalTokens` | int64 | yes | (payload) | `tokenops.rule.total_tokens` | ≥0; sum of section tokens |
| `TotalChars` | *int64 | no | (payload) | — | ≥0 |
| `Hash` | *string | no | (payload) | — | SHA-256 hex prefixed `sha256:` |
| `Sections[].ID` | string | yes | (payload) | `tokenops.rule.section_id` | `SourceID#Anchor` |
| `Sections[].Anchor` | *string | no | (payload) | — | Heading path, slash-joined |
| `Sections[].TokenCount` | int64 | yes | (payload) | — | ≥0; measured under Tokenizer |
| `Sections[].Hash` | *string | no | (payload) | — | SHA-256 hex |
| `IngestedAt` | time.Time | no | (payload) | — | UTC; defaults to envelope timestamp |

### Invariants
- `SourceID` is stable across snapshots of the same artifact.
- `Sections[].ID` follows the format `SourceID + "#" + Anchor` (Anchor `_preamble` for content before any heading).
- `Hash` of the document equals SHA-256 of the raw body; same body across providers yields the same hash.
- Raw body text never appears in the event — only metrics, anchors, and hashes — so redaction is inherent.

---

## 7. RuleAnalysisEvent Contract

**File:** `pkg/eventschema/rule.go`
**SQLite columns:** (payload only; not indexed)
**OTLP:** `tokenops.rule.*` keys in `pkg/eventschema/otel.go`

| Field | Type | Required | SQLite | OTLP | Constraints |
|---|---|---|---|---|---|
| `SourceID` | string | yes | (payload) | `tokenops.rule.source_id` | Must match a previously observed `RuleSourceEvent.SourceID` |
| `SectionID` | *string | no | (payload) | `tokenops.rule.section_id` | Empty = document-level rollup |
| `WorkflowID` | *string | no | (payload) | `tokenops.workflow.id` | Attribution |
| `AgentID` | *string | no | (payload) | `tokenops.agent.id` | Attribution |
| `WindowStart` | time.Time | yes | (payload) | — | Closed-interval start |
| `WindowEnd` | time.Time | yes | (payload) | — | Closed-interval end; ≥ WindowStart |
| `Exposures` | int64 | yes | (payload) | `tokenops.rule.exposures` | ≥0 |
| `ContextTokens` | int64 | yes | (payload) | `tokenops.rule.context_tokens` | ≥0; cumulative across window |
| `TokensSaved` | *int64 | no | (payload) | `tokenops.rule.tokens_saved` | ≥0 (clamped) |
| `RetriesAvoided` | *int64 | no | (payload) | `tokenops.rule.retries_avoided` | ≥0 (clamped) |
| `ContextReduction` | *float64 | no | (payload) | `tokenops.rule.context_reduction` | Range [-1.0, 1.0]; negative = growth |
| `LatencyImpactNS` | *int64 | no | (payload) | `tokenops.latency_ns` | Sign carries direction |
| `QualityDelta` | *float64 | no | (payload) | `tokenops.rule.quality_delta` | Range [-1.0, 1.0] |
| `ROIScore` | *float64 | no | (payload) | `tokenops.rule.roi_score` | `(TokensSaved - ContextTokens) / ContextTokens`; 0 when ContextTokens==0 |
| `ConflictsWith` | []string | no | (payload) | — | SectionIDs flagged by conflict detector |
| `RedundantWith` | []string | no | (payload) | — | SectionIDs flagged by dedupe analyzer |
| `CompressedTokens` | *int64 | no | (payload) | `tokenops.rule.compressed_tokens` | ≥0; 0 means no compression run |

### Invariants
- `SourceID` MUST match a known `RuleSourceEvent.SourceID`.
- `WindowEnd >= WindowStart`.
- `TokensSaved` and `RetriesAvoided` are floor-clamped to 0 by `ROIEngine`.
- `ROIScore` is normalised by `ContextTokens`; comparable across rules of different sizes.
- `ConflictsWith` / `RedundantWith` carry stable SectionIDs, never raw body text.

---

## SQLite Schema Contract

**File:** `internal/storage/sqlite/schema.go`

| Migration | Table | Invariant |
|---|---|---|
| v1 | `events` | `id` is UUIDv7, globally unique |
| v1 | `events` | `type` + `day` indexes are present |
| v1 | `events` | `payload` is valid JSON matching the event type |
| v2 | `audit_log` | Append-only; no UPDATEs |

### Invariants
- All SELECT queries use indexed columns for filtering.
- `payload` is the canonical representation; indexed columns are a
  query-optimised projection.
- Storage guarantees at-most-once delivery via `ON CONFLICT(id) DO NOTHING`.

## OTLP Attribute Contract

**File:** `pkg/eventschema/otel.go`, `internal/otlp/exporter.go`

- All `gen_ai.*` keys follow OpenTelemetry GenAI semantic conventions
  where applicable.
- All `tokenops.*` keys are prefixed with the project namespace.
- Int64 values are encoded as decimal strings in JSON OTLP wire format.
- Envelope-level attributes are passed through as-is (no prefix).
- Redaction runs before OTLP export: `redaction.RedactEnvelope(env)`.

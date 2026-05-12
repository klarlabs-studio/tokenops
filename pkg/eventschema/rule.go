package eventschema

import "time"

// RuleSource identifies the origin kind of an operational rule artifact.
// Rule artifacts are persistent behavioral scaffolding (CLAUDE.md, AGENTS.md,
// Cursor rules, MCP policies, repo conventions) that TokenOps Rule
// Intelligence treats as first-class telemetry — measured, deduplicated,
// compressed, and dynamically injected.
type RuleSource string

// Known rule sources.
const (
	RuleSourceUnknown        RuleSource = "unknown"
	RuleSourceClaudeMD       RuleSource = "claude_md"
	RuleSourceAgentsMD       RuleSource = "agents_md"
	RuleSourceCursorRules    RuleSource = "cursor_rules"
	RuleSourceMCPPolicy      RuleSource = "mcp_policy"
	RuleSourceRepoConvention RuleSource = "repo_convention"
	RuleSourceCustom         RuleSource = "custom"
)

// RuleScope identifies the granularity at which a rule operates.
type RuleScope string

// Known rule scopes.
const (
	RuleScopeUnknown     RuleScope = "unknown"
	RuleScopeGlobal      RuleScope = "global"
	RuleScopeRepo        RuleScope = "repo"
	RuleScopeWorkflow    RuleScope = "workflow"
	RuleScopeTool        RuleScope = "tool"
	RuleScopeFileGlob    RuleScope = "file_glob"
	RuleScopeConditional RuleScope = "conditional"
)

// RuleSection represents a single addressable block within a rule document.
// Sections are the unit of measurement, ROI attribution, conflict detection,
// compression, and dynamic injection.
type RuleSection struct {
	// ID is a stable identifier derived from the source path + anchor, used to
	// correlate measurements across snapshots of the same rule corpus.
	ID string `json:"id"`
	// Anchor is the human-readable heading path (e.g. "Testing/TDD") that
	// addresses this section within its parent document.
	Anchor string `json:"anchor,omitempty"`
	// TokenCount is the section's token cost in the provider tokenizer the
	// source was measured against (see RuleSourceEvent.Tokenizer).
	TokenCount int64 `json:"token_count"`
	// CharCount is the raw character length, useful for tokenizer-independent
	// size baselines.
	CharCount int64 `json:"char_count,omitempty"`
	// Hash is a content hash of the section body (sha256:hex), used to detect
	// drift between snapshots without persisting raw text.
	Hash string `json:"hash,omitempty"`
}

// RuleSourceEvent captures the ingestion of a rule artifact: identity, size,
// taxonomy, and per-section breakdown. One RuleSourceEvent is emitted per
// observed snapshot of the artifact. Raw rule text is never carried in the
// event — only metrics, hashes, and section anchors — so redaction is
// inherent. Authoritative content (when needed) lives in the local rule
// store.
type RuleSourceEvent struct {
	// SourceID is a stable identifier for the rule artifact (e.g. an absolute
	// path + repo identifier hash). Same value across snapshots.
	SourceID string `json:"source_id"`
	// Source is the artifact's classification kind.
	Source RuleSource `json:"source"`
	// Scope is the artifact's operational granularity.
	Scope RuleScope `json:"scope,omitempty"`

	// Path is the relative path of the artifact within its repo (e.g.
	// "CLAUDE.md", ".cursor/rules/go.mdc"). Optional; redacted when sensitive.
	Path string `json:"path,omitempty"`
	// RepoID is an opaque identifier for the originating repository, when
	// known. Allows cross-repo rule intelligence without leaking repo names.
	RepoID string `json:"repo_id,omitempty"`

	// Tokenizer identifies the tokenizer used to measure TokenCount values
	// in this event ("openai/cl100k_base", "anthropic", "gemini").
	Tokenizer string `json:"tokenizer,omitempty"`
	// Provider is the LLM provider whose tokenizer was used. The same rule
	// artifact may be re-measured under multiple providers.
	Provider Provider `json:"provider,omitempty"`

	// TotalTokens is the artifact-level token cost (sum of section tokens).
	TotalTokens int64 `json:"total_tokens"`
	// TotalChars is the artifact-level character length.
	TotalChars int64 `json:"total_chars,omitempty"`
	// Hash is the artifact-level content hash (sha256:hex).
	Hash string `json:"hash,omitempty"`

	// Sections decomposes the artifact into addressable blocks.
	Sections []RuleSection `json:"sections,omitempty"`

	// IngestedAt records when this snapshot was observed.
	IngestedAt time.Time `json:"ingested_at,omitzero"`
}

// Type identifies this payload as a RuleSourceEvent.
func (*RuleSourceEvent) Type() EventType { return EventTypeRuleSource }

// RuleAnalysisEvent reports the operational ROI of a rule artifact or one of
// its sections over a measurement window. Analysis events are produced by
// the rule intelligence engine (offline or batch), not by request-path
// emitters. They feed dashboards, the rule-router relevance model, and the
// rule benchmarking harness.
type RuleAnalysisEvent struct {
	// SourceID identifies the rule artifact under analysis. Must match a
	// previously observed RuleSourceEvent.SourceID.
	SourceID string `json:"source_id"`
	// SectionID optionally narrows the analysis to a single section; empty
	// means artifact-level rollup.
	SectionID string `json:"section_id,omitempty"`

	// WorkflowID and AgentID attribute the analysis window to a specific
	// workflow or agent, enabling per-context ROI breakdowns.
	WorkflowID string `json:"workflow_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`

	// WindowStart and WindowEnd bound the measurement window. Both
	// inclusive (closed interval).
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`

	// Exposures counts how many requests included this rule (or section)
	// in their context across the window.
	Exposures int64 `json:"exposures"`
	// ContextTokens is the cumulative token cost of carrying this rule in
	// request contexts across the window.
	ContextTokens int64 `json:"context_tokens"`

	// TokensSaved is the estimated downstream token reduction attributable
	// to this rule's presence (e.g. reduced retries, shorter outputs,
	// avoided rewrites). Estimation methodology lives in the analyzer.
	TokensSaved int64 `json:"tokens_saved,omitempty"`
	// RetriesAvoided is the estimated number of retries the rule prevented
	// across the window.
	RetriesAvoided int64 `json:"retries_avoided,omitempty"`
	// ContextReduction is the fraction by which downstream context grew
	// less than baseline (0.0 to 1.0; negative if growth was higher).
	ContextReduction float64 `json:"context_reduction,omitempty"`
	// LatencyImpactNS is the cumulative latency change attributable to the
	// rule (negative reduces latency, positive increases it).
	LatencyImpactNS int64 `json:"latency_impact_ns,omitempty"`
	// QualityDelta is the change in optimization quality score versus the
	// rule-absent baseline. Range -1.0 to 1.0.
	QualityDelta float64 `json:"quality_delta,omitempty"`

	// ROIScore is the engine's composite return-on-investment for this rule
	// over the window. Higher is better; 0.0 means break-even versus its
	// context cost.
	ROIScore float64 `json:"roi_score,omitempty"`

	// ConflictsWith lists SourceID/SectionID pairs that the conflict
	// detector flagged as semantically opposing this rule.
	ConflictsWith []string `json:"conflicts_with,omitempty"`
	// RedundantWith lists SourceID/SectionID pairs the dedupe analyzer
	// flagged as overlapping.
	RedundantWith []string `json:"redundant_with,omitempty"`

	// CompressedTokens reports the token cost of the rule after the
	// compression pipeline; zero means no compression was applied.
	CompressedTokens int64 `json:"compressed_tokens,omitempty"`
}

// Type identifies this payload as a RuleAnalysisEvent.
func (*RuleAnalysisEvent) Type() EventType { return EventTypeRuleAnalysis }

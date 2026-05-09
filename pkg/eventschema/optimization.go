package eventschema

// OptimizationType identifies which optimizer produced a recommendation or
// applied a change.
type OptimizationType string

// Known optimization types — mirrors the optimizer pipeline stages.
const (
	OptimizationTypeUnknown        OptimizationType = "unknown"
	OptimizationTypePromptCompress OptimizationType = "prompt_compress"
	OptimizationTypeDedupe         OptimizationType = "semantic_dedupe"
	OptimizationTypeRetrievalPrune OptimizationType = "retrieval_prune"
	OptimizationTypeContextTrim    OptimizationType = "context_trim"
	OptimizationTypeSystemDedupe   OptimizationType = "system_dedupe"
	OptimizationTypeRouter         OptimizationType = "model_router"
	OptimizationTypeCacheReuse     OptimizationType = "cache_reuse"
)

// OptimizationMode describes how the optimization was offered or applied.
type OptimizationMode string

// Known optimization modes.
const (
	OptimizationModeUnknown     OptimizationMode = "unknown"
	OptimizationModePassive     OptimizationMode = "passive"
	OptimizationModeInteractive OptimizationMode = "interactive"
	OptimizationModeReplay      OptimizationMode = "replay"
)

// OptimizationDecision records the user/system decision on a recommendation.
type OptimizationDecision string

// Known decisions.
const (
	OptimizationDecisionUnknown  OptimizationDecision = "unknown"
	OptimizationDecisionApplied  OptimizationDecision = "applied"
	OptimizationDecisionAccepted OptimizationDecision = "accepted"
	OptimizationDecisionRejected OptimizationDecision = "rejected"
	OptimizationDecisionSkipped  OptimizationDecision = "skipped"
)

// OptimizationEvent captures the outcome of an optimizer pass over a request.
type OptimizationEvent struct {
	// PromptHash links the optimization to the originating PromptEvent.
	PromptHash string `json:"prompt_hash"`

	Type OptimizationType `json:"type"`
	Mode OptimizationMode `json:"mode"`

	// EstimatedSavingsTokens and EstimatedSavingsUSD are the optimizer's
	// projected reduction. They are estimates, not actuals — actual savings
	// are measured in replay or by comparing pre/post PromptEvents.
	EstimatedSavingsTokens int64   `json:"estimated_savings_tokens"`
	EstimatedSavingsUSD    float64 `json:"estimated_savings_usd,omitempty"`

	// QualityScore is the optimizer's predicted quality preservation (0.0 to
	// 1.0). Values below the configured threshold cause the quality gate to
	// reject the optimization with Decision = OptimizationDecisionSkipped.
	QualityScore float64 `json:"quality_score,omitempty"`

	Decision OptimizationDecision `json:"decision"`
	// Reason carries an optimizer-defined explanation, especially when
	// Decision is rejected or skipped (e.g. "quality_below_threshold").
	Reason string `json:"reason,omitempty"`

	// LatencyImpact is the additional latency the optimizer added to the
	// request path (negative if it reduced latency, e.g. via cache reuse).
	LatencyImpactNS int64 `json:"latency_impact_ns,omitempty"`

	// WorkflowID and AgentID propagate workflow attribution so optimizations
	// roll up alongside their owning workflow.
	WorkflowID string `json:"workflow_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
}

func (*OptimizationEvent) eventType() EventType { return EventTypeOptimization }

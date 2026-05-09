package eventschema

// CoachingRecommendationKind classifies a coaching recommendation.
type CoachingRecommendationKind string

// Known coaching recommendation kinds.
const (
	CoachingKindUnknown          CoachingRecommendationKind = "unknown"
	CoachingKindReducePromptSize CoachingRecommendationKind = "reduce_prompt_size"
	CoachingKindTrimContext      CoachingRecommendationKind = "trim_context"
	CoachingKindShrinkRetrieval  CoachingRecommendationKind = "shrink_retrieval"
	CoachingKindReuseCache       CoachingRecommendationKind = "reuse_cache"
	CoachingKindSwitchModel      CoachingRecommendationKind = "switch_model"
	CoachingKindBatchRequests    CoachingRecommendationKind = "batch_requests"
	CoachingKindBreakRecursion   CoachingRecommendationKind = "break_recursion"
)

// CoachingEvent carries a single coaching recommendation produced by replay
// analysis or the live waste-pattern detector.
type CoachingEvent struct {
	// SessionID identifies the analysed session (typically a workflow or a
	// span of related PromptEvents).
	SessionID string `json:"session_id"`
	// WorkflowID and AgentID propagate attribution.
	WorkflowID string `json:"workflow_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`

	Kind CoachingRecommendationKind `json:"kind"`

	// Summary is a short, human-readable headline.
	Summary string `json:"summary"`
	// Details provides the longer explanation.
	Details string `json:"details,omitempty"`

	// EstimatedSavingsTokens and EstimatedSavingsUSD are the projected
	// reduction if the recommendation is adopted.
	EstimatedSavingsTokens int64   `json:"estimated_savings_tokens,omitempty"`
	EstimatedSavingsUSD    float64 `json:"estimated_savings_usd,omitempty"`

	// EfficiencyScore is the user/workflow score that produced this
	// recommendation, on a 0.0–1.0 scale (higher is better).
	EfficiencyScore float64 `json:"efficiency_score,omitempty"`
	// EfficiencyDelta is the score's change versus the previous evaluation.
	EfficiencyDelta float64 `json:"efficiency_delta,omitempty"`

	// ReplayMetadata carries optional pointers to the replayed session
	// (e.g. event store IDs) so the dashboard or CLI can fetch detail.
	ReplayMetadata map[string]string `json:"replay_metadata,omitempty"`

	// Decision records whether the user adopted the recommendation. May be
	// empty for newly generated, undecided recommendations.
	Decision OptimizationDecision `json:"decision,omitempty"`
}

func (*CoachingEvent) eventType() EventType { return EventTypeCoaching }

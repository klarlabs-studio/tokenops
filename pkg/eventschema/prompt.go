package eventschema

import "time"

// CostSource identifies how the request's spend should be accounted.
// Metered is the default — per-token billing through the upstream
// provider's API. Plan-based subscriptions (Claude Max, ChatGPT Plus,
// Copilot, Cursor) are flat-rate, so their CostUSD is zero and the
// PromptEvent counts toward a plan quota instead. Trial covers
// provider-issued free credits with no quota tracking.
type CostSource string

// Known cost sources. Empty string deserialises as CostSourceMetered
// so prior envelopes round-trip without modification.
const (
	CostSourceMetered      CostSource = "metered"
	CostSourcePlanIncluded CostSource = "plan_included"
	CostSourceTrial        CostSource = "trial"
)

// TokenSource identifies where a PromptEvent's token counts came from.
//
// It exists for the same reason CostSource does: the numbers beside it
// are int64s, and an int64 cannot say "nobody counted these". A proxy
// with no tokenizer wired ships events with zero counts by documented
// design, and downstream that is indistinguishable from a request that
// genuinely used no tokens — so a deployment that forgot the tokenizer
// reports zero usage as confidently as one that measured it.
type TokenSource string

// Known token sources. The empty string deserialises as
// TokenSourceCounted so envelopes written before this field existed
// round-trip unchanged: their counts were produced by a tokenizer, which
// is what the zero value now means.
const (
	// TokenSourceCounted — a tokenizer counted the request or response.
	TokenSourceCounted TokenSource = "counted"
	// TokenSourceVendorReported — the provider reported the counts in its
	// response, which is better evidence than counting locally.
	TokenSourceVendorReported TokenSource = "vendor_reported"
	// TokenSourceEstimated — derived from a heuristic (bytes/4 and the
	// like) rather than counted.
	TokenSourceEstimated TokenSource = "estimated"
	// TokenSourceUncounted — nothing counted these. The counts beside
	// this are zeros standing in for an absent measurement, not an
	// observation that nothing was consumed.
	TokenSourceUncounted TokenSource = "uncounted"
)

// PromptEvent captures a single LLM request/response cycle observed by the
// TokenOps proxy. Token counts are filled by the per-provider tokenizer.
type PromptEvent struct {
	// PromptHash is a content hash of the canonicalised request body, used
	// for deduplication and replay without storing the raw prompt.
	PromptHash string `json:"prompt_hash"`

	// Provider is the upstream LLM provider (openai, anthropic, gemini, ...).
	Provider Provider `json:"provider"`
	// RequestModel is the model the client requested.
	RequestModel string `json:"request_model"`
	// ResponseModel is the model the provider reported in its response, when
	// it differs from RequestModel (e.g. version pinning, routing).
	ResponseModel string `json:"response_model,omitempty"`

	// InputTokens is the number of tokens consumed by the request (prompt).
	InputTokens int64 `json:"input_tokens"`
	// OutputTokens is the number of tokens produced by the response.
	OutputTokens int64 `json:"output_tokens"`
	// TotalTokens is InputTokens + OutputTokens.
	TotalTokens int64 `json:"total_tokens"`
	// CachedInputTokens, when the provider reports cache hits, captures the
	// portion of input tokens served from the provider-side prompt cache.
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`

	// ContextSize is the number of tokens of context (system + history)
	// included in the request.
	ContextSize int64 `json:"context_size"`
	// MaxOutputTokens is the requested response budget, when set.
	MaxOutputTokens int64 `json:"max_output_tokens,omitempty"`

	// Latency is the wall-clock time from the proxy receiving the request to
	// finishing the response (including streaming).
	Latency time.Duration `json:"latency_ns"`
	// TimeToFirstToken is the time until the first response token was
	// observed (zero for non-streaming responses).
	TimeToFirstToken time.Duration `json:"time_to_first_token_ns,omitempty"`

	// Streaming reports whether the response was an SSE stream.
	Streaming bool `json:"streaming"`
	// Status is the upstream HTTP status (or zero if the request failed
	// before the upstream responded).
	Status int `json:"status"`
	// FinishReason mirrors the provider's stop/finish reason when present
	// (e.g. "stop", "length", "tool_use").
	FinishReason string `json:"finish_reason,omitempty"`
	// ToolCallCount is the number of tool-call output items reported by the
	// provider. It records only the count, never tool names, arguments, or
	// results, so coding-agent behavior is measurable without retaining content.
	ToolCallCount int64 `json:"tool_call_count,omitempty"`
	// ErrorCode is set when Status indicates an error response.
	ErrorCode string `json:"error_code,omitempty"`

	// CacheHit indicates whether the TokenOps response cache served this
	// request (mutually exclusive with reaching the upstream provider).
	CacheHit bool `json:"cache_hit,omitempty"`

	// CostUSD is the monetary cost computed from the spend engine pricing
	// table at event time (informational — authoritative recompute lives in
	// the analytics pipeline).
	CostUSD float64 `json:"cost_usd,omitempty"`
	// CostMeasured is true only when a metered cost was successfully
	// computed with an event-time rate card. Zero can therefore be a priced
	// zero; false means cost is absent or belongs to a non-metered resource.
	CostMeasured bool `json:"cost_measured,omitempty"`

	// TokenSource says where the token counts above came from. Empty
	// (default) means a tokenizer counted them. Read TokensCounted before
	// treating a zero as an observation.
	TokenSource TokenSource `json:"token_source,omitempty"`

	// CostSource identifies how this request was billed. Empty (default)
	// means metered per-token billing. Plan-included requests roll up to
	// a subscription quota rather than CostUSD; see internal/contexts/
	// spend/plans for catalog + headroom semantics.
	CostSource CostSource `json:"cost_source,omitempty"`

	// WorkflowID, AgentID, and SessionID provide attribution. They are
	// populated by clients via headers or by the proxy from contextual
	// signals; absence implies a single-shot, untracked invocation.
	WorkflowID string `json:"workflow_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	// UserID is an opaque, optionally hashed user identifier.
	UserID string `json:"user_id,omitempty"`
}

// Type identifies this payload as a PromptEvent.
func (*PromptEvent) Type() EventType { return EventTypePrompt }

// TokenProvenance returns the event's TokenSource, resolving the empty
// default to TokenSourceCounted.
func (p PromptEvent) TokenProvenance() TokenSource {
	if p.TokenSource == "" {
		return TokenSourceCounted
	}
	return p.TokenSource
}

// TokensCounted reports whether the token counts reflect an actual
// measurement. False means the zeros beside it stand in for a
// measurement nobody made, and a surface reporting usage should say so
// rather than show them.
func (p PromptEvent) TokensCounted() bool {
	return p.TokenProvenance() != TokenSourceUncounted
}

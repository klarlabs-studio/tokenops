package eventschema

import "time"

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
	// ErrorCode is set when Status indicates an error response.
	ErrorCode string `json:"error_code,omitempty"`

	// CacheHit indicates whether the TokenOps response cache served this
	// request (mutually exclusive with reaching the upstream provider).
	CacheHit bool `json:"cache_hit,omitempty"`

	// CostUSD is the monetary cost computed from the spend engine pricing
	// table at event time (informational — authoritative recompute lives in
	// the analytics pipeline).
	CostUSD float64 `json:"cost_usd,omitempty"`

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

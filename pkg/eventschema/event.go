package eventschema

import "time"

// EventType identifies the kind of event carried by an Envelope.
type EventType string

// Known event types. Add new values as additive (minor) version bumps.
const (
	EventTypeUnknown      EventType = "unknown"
	EventTypePrompt       EventType = "prompt"
	EventTypeWorkflow     EventType = "workflow"
	EventTypeOptimization EventType = "optimization"
	EventTypeCoaching     EventType = "coaching"
	EventTypeRuleSource   EventType = "rule_source"
	EventTypeRuleAnalysis EventType = "rule_analysis"
	EventTypeDecision     EventType = "decision"
	EventTypeOutcome      EventType = "outcome"
	EventTypeExperiment   EventType = "experiment"
	EventTypeDomain       EventType = "domain"
)

// Provider identifies the upstream LLM provider observed for an event.
type Provider string

// Known providers.
const (
	ProviderUnknown   Provider = "unknown"
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderGemini    Provider = "gemini"
	ProviderMistral   Provider = "mistral"
	ProviderGitHub    Provider = "github"
	ProviderCursor    Provider = "cursor"

	// OpenAI-compatible upstreams — they accept the OpenAI
	// /chat/completions request shape, so the proxy meters them with the
	// shared OpenAI normalizer and tokenizer.
	ProviderGroq       Provider = "groq"
	ProviderDeepSeek   Provider = "deepseek"
	ProviderXAI        Provider = "xai"
	ProviderPerplexity Provider = "perplexity"
	ProviderFireworks  Provider = "fireworks"
	ProviderCerebras   Provider = "cerebras"
	ProviderTogether   Provider = "together"
	ProviderOpenRouter Provider = "openrouter"

	// Cohere — its own chat wire format (v2 /v2/chat messages array, v1
	// /v1/chat message + chat_history), so it has a dedicated normalizer
	// rather than the shared OpenAI one.
	ProviderCohere Provider = "cohere"

	// Local runtimes and self-hosted gateways — all expose an
	// OpenAI-compatible /v1/chat/completions surface. Their presets point
	// at each tool's documented default host/port; override per install.
	ProviderOllama   Provider = "ollama"
	ProviderLMStudio Provider = "lmstudio"
	ProviderLiteLLM  Provider = "litellm"
	ProviderVercel   Provider = "vercel"
)

// Association ties an event to the work it was part of.
//
// The envelope already carried WorkflowID, AgentID, SessionID and
// UserID — attribution invented before the ontology existed, and none of
// it says which *attempt* at which *goal* produced the event. So the
// Work, Execution and Actor introduced in Phase 3 had nothing to attach
// to, and no event could be rolled up to a goal a person would
// recognise.
//
// Every field is optional and they are filled in independently. The
// proxy may know the actor from a request header long before anything
// knows the goal; requiring all three would mean discarding the part
// that is known. The older attribution fields stay where they are —
// they are what every existing query uses — and this is added beside
// them rather than replacing them.
type Association struct {
	// Work is the goal this event was in service of.
	Work string `json:"work,omitempty"`
	// Execution is the attempt at that goal.
	Execution string `json:"execution,omitempty"`
	// Actor is who or what was working.
	Actor string `json:"actor,omitempty"`
}

// Correlation joins the records in a decision, intervention and experiment
// lifecycle without overloading tracing identifiers.
type Correlation struct {
	Decision     string `json:"decision,omitempty"`
	Intervention string `json:"intervention,omitempty"`
	Experiment   string `json:"experiment,omitempty"`
}

// Empty reports whether no control-plane lifecycle is associated.
func (c Correlation) Empty() bool {
	return c.Decision == "" && c.Intervention == "" && c.Experiment == ""
}

// Empty reports whether nothing is associated.
func (a Association) Empty() bool {
	return a.Work == "" && a.Execution == "" && a.Actor == ""
}

// Envelope is the common header carried by every TokenOps event regardless of
// payload type. The Payload field carries the type-specific body.
type Envelope struct {
	// ID is a globally unique identifier (UUIDv7 recommended) for this event.
	ID string `json:"id"`
	// SchemaVersion captures the eventschema version that produced this event.
	SchemaVersion string `json:"schema_version"`
	// Type identifies the payload variant.
	Type EventType `json:"type"`
	// Timestamp is the event occurrence time in UTC.
	Timestamp time.Time `json:"timestamp"`
	// TraceID and SpanID, when present, link the event to a distributed trace
	// (W3C trace-context format).
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`
	// Source identifies the emitting component (e.g. "proxy", "optimizer").
	Source string `json:"source,omitempty"`
	// Attributes carries additional OpenTelemetry-style key/value attributes
	// that do not fit the typed payload (e.g. tenant tags, deployment labels).
	Attributes map[string]string `json:"attributes,omitempty"`
	// Association ties this event to the work, attempt and actor it
	// belongs to. Empty on every event written before the ontology
	// existed, and on most live traffic until something reconstructs
	// work from it — which is a gap to be filled, not a default to be
	// invented.
	Association Association `json:"association,omitzero"`
	// Correlation ties the explanation, action, outcome and experiment
	// together. It is distinct from distributed tracing.
	Correlation Correlation `json:"correlation,omitzero"`
	// Payload is one of the typed event payloads. The concrete type is
	// determined by Type.
	Payload Payload `json:"payload"`
}

// Associated reports whether this event is tied to any part of the work
// ontology.
//
// False is the honest answer for most events today and must stay
// distinguishable from an association to an empty work: "we do not know
// what this was for" and "this was for nothing" are different claims.
func (e Envelope) Associated() bool { return !e.Association.Empty() }

// Payload is the interface satisfied by all typed event payloads. The Type
// method returns the EventType discriminator that identifies the concrete
// payload — callers (e.g. the storage layer) use it to dispatch decoders.
type Payload interface {
	Type() EventType
}

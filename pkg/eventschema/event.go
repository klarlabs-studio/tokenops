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
)

// Provider identifies the upstream LLM provider observed for an event.
type Provider string

// Known providers.
const (
	ProviderUnknown   Provider = "unknown"
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderGemini    Provider = "gemini"
)

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
	// Payload is one of *PromptEvent, *WorkflowEvent, *OptimizationEvent,
	// *CoachingEvent. The concrete type is determined by Type.
	Payload Payload `json:"payload"`
}

// Payload is the marker interface satisfied by all typed event payloads.
type Payload interface {
	eventType() EventType
}

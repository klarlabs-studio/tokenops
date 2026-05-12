// Package eventschema defines the canonical TokenOps event types emitted by
// the proxy, optimization engine, observability platform, and coaching engine.
//
// All events share a common Envelope and are versioned via SchemaVersion. The
// schema is OpenTelemetry-compatible: attribute keys follow the GenAI
// semantic conventions (gen_ai.*) where applicable, augmented with TokenOps
// specific keys (tokenops.*).
//
// The Protobuf source of truth lives at pkg/eventschema/proto/v1/events.proto.
// The Go types in this package mirror the Protobuf schema; they are the
// public API consumed by SDKs, integrations, and the storage layer.
//
// # Immutability contract
//
// Payload structs (PromptEvent, WorkflowEvent, OptimizationEvent,
// CoachingEvent, RuleSourceEvent, RuleAnalysisEvent) expose public
// fields for ergonomic construction at emit time. After a payload is
// wrapped in an Envelope and handed to events.Bus or
// domainevents.Bus, callers MUST NOT mutate it — subscribers may run
// concurrently and rely on a stable view.
//
// The Envelope.Clone helper produces a defensive copy suitable when an
// adapter does need to mutate (e.g. redaction transforms). Subscribers
// that want full guarantee should Clone before any read+modify cycle.
package eventschema

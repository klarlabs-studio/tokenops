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
package eventschema

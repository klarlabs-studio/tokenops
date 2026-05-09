package providers

import "github.com/felixgeelhaar/tokenops/pkg/eventschema"

// CanonicalRequest is the provider-agnostic shape extracted from an inference
// request. It carries the minimum fields required by the event pipeline,
// optimizer, and CLI replay; provider-specific extras are not preserved here
// (the raw request body remains available alongside).
type CanonicalRequest struct {
	Provider  eventschema.Provider
	Operation string // e.g. "chat.completions", "messages", "generate_content"

	Model           string
	Stream          bool
	MaxOutputTokens int64

	MessageCount  int
	SystemPresent bool
}

// NormalizeFunc converts a raw request (path under provider prefix and body
// bytes) into a CanonicalRequest. Implementations should be tolerant of
// missing or unknown fields and avoid allocating beyond what is necessary
// for canonicalisation — they run on the request hot path.
type NormalizeFunc func(path string, body []byte) (CanonicalRequest, error)

package pricing

import "context"

// Source is a pluggable provider of a pricing Snapshot. Implementations fetch
// from a machine-readable feed (LiteLLM today; OpenRouter, a vendor-page
// scraper, or a curated override later) and normalize to the internal
// Snapshot model. Keeping the engine behind this interface is the ADR 0002
// decision: adding a source never touches the cost path.
type Source interface {
	// Name is the stable identifier used by `pricing refresh --source <name>`.
	Name() string
	// Fetch retrieves the current rate card. It must honour ctx (deadline /
	// cancellation) and return a wrapped ErrFetch on any network or parse
	// failure so the caller can fall back to the baseline without writing.
	Fetch(ctx context.Context) (Snapshot, error)
}

// SourceByName returns the built-in Source for name, or nil when unknown.
// Phase 1 ships only "litellm"; Phase 3 adds more. url overrides the source's
// default endpoint when non-empty (used for `--url` and, in tests, an
// httptest server).
func SourceByName(name, url string) Source {
	switch name {
	case "", "litellm":
		s := NewLiteLLMSource()
		if url != "" {
			s.URL = url
		}
		return s
	default:
		return nil
	}
}

package pricing

import (
	"context"
	"strings"
)

// Source is a pluggable provider of a pricing Snapshot. Implementations fetch
// from a machine-readable feed (LiteLLM and models.dev today, in
// internal/infra/pricingsource; OpenRouter, a vendor-page scraper, or a
// curated override later) and normalize to the internal Snapshot model with
// ParseLiteLLM / ParseModelsDev. Keeping the engine behind this interface is the ADR 0002
// decision: adding a source never touches the cost path.
type Source interface {
	// Name is the stable identifier used by `pricing refresh --source <name>`.
	Name() string
	// Fetch retrieves the current rate card. It must honour ctx (deadline /
	// cancellation) and return a wrapped ErrFetch on any network or parse
	// failure so the caller can fall back to the baseline without writing.
	Fetch(ctx context.Context) (Snapshot, error)
}

// Combined fetches several sources into one snapshot: LiteLLM for the
// model vendors, models.dev for the gateways (ADR 0009 §6). Their keys do
// not overlap, since each prices different providers. Any one failing
// fails the whole fetch, so the card in force keeps pricing rather than
// a snapshot that silently lost one source's rows.
type Combined []Source

// Name implements Source.
func (c Combined) Name() string {
	names := make([]string, 0, len(c))
	for _, s := range c {
		names = append(names, s.Name())
	}
	return strings.Join(names, "+")
}

// Fetch implements Source.
func (c Combined) Fetch(ctx context.Context) (Snapshot, error) {
	out := Snapshot{Source: c.Name(), Rates: map[string]Rate{}}
	for _, s := range c {
		snap, err := s.Fetch(ctx)
		if err != nil {
			return Snapshot{}, err
		}
		if snap.FetchedAt.After(out.FetchedAt) {
			out.FetchedAt = snap.FetchedAt
		}
		for k, r := range snap.Rates {
			if _, taken := out.Rates[k]; !taken {
				out.Rates[k] = r
			}
		}
	}
	return out, nil
}

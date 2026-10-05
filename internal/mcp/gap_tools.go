package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/spending"

	"go.klarlabs.de/tokenops/internal/capability/state"

	"go.klarlabs.de/tokenops/internal/config"
)

// GapDeps wires the two surfaces that were CLI-only.
//
// Both answer questions an agent has and could not ask. `pricing` is what a
// model costs and whether the card has moved under it; `vendor-usage status`
// is whether the numbers it is about to quote are being fed at all.
// tokenops_status (view=data_sources) reports event counts, which tells an agent that a
// source is silent but not whether it was ever switched on — the difference
// between "no data" and "no data because nothing is configured".
type GapDeps struct {
	// Config is the active configuration. Nil disables the vendor-usage
	// tool rather than reporting an empty one, because "no sources" and
	// "no config loaded" are different answers.
	Config *config.Config
	// ConfigGetter returns the live configuration at call time and takes
	// precedence over Config. tokenops_configure (setting=usage_meter) switches sources
	// on by rewriting config.yaml under a running server; reading the
	// startup snapshot, this tool kept reporting them off until the MCP
	// client restarted. A nil result disables the tool, like a nil Config.
	ConfigGetter func() *config.Config
	// Counts returns events per source tag over a window.
	Counts func(ctx context.Context, since, until time.Time) (map[string]int64, error)
	// PricingDir is where rate snapshots live; empty uses the default.
	PricingDir string
}

// activeConfig prefers the live getter and falls back to the static
// snapshot for callers that wire no watcher.
func (d GapDeps) activeConfig() *config.Config {
	if d.ConfigGetter != nil {
		return d.ConfigGetter()
	}
	return d.Config
}

type pricingInput struct {
	Provider string `json:"provider,omitempty" jsonschema:"description=filter to one provider, e.g. anthropic"`
	Model    string `json:"model,omitempty" jsonschema:"description=substring match on the model name"`
	Limit    int    `json:"limit,omitempty" jsonschema:"description=max rows; default 20"`
}

// pricingResult is the spending capability's payload, shared with the
// daemon API (ADR 0010 §4).
type pricingResult = spending.Rates

type vendorUsageInput struct {
	WindowHours int `json:"window_hours,omitempty" jsonschema:"description=lookback in hours; default 24"`
}

// vendorUsageResult is the state capability's payload, shared with the
// daemon API (ADR 0010 §4).
type vendorUsageResult = state.VendorUsage

// RegisterGapTools mounts the tools that close the CLI-only side of the
// parity allowlist.
func RegisterGapTools(s *Server, d GapDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}

	s.Tool("tokenops_pricing").
		Description("Return the current per-million-token rates TokenOps costs with, and when that card was fetched. Call it to answer what a model costs, to compare two models before routing work to one, or to check whether a rate moved under a figure you already quoted. Rates are pinned per model with a dated source, and the snapshot says when it was fetched — a figure quoted from a card refreshed days ago is worth re-checking before acting on it.").
		OutputSchema(pricingResult{}).
		Handler(func(_ context.Context, in pricingInput) (*pricingResult, error) {
			res := spending.RateCard(d.PricingDir, spending.RateQuery{Provider: in.Provider, Model: in.Model, Limit: in.Limit})
			return &res, nil
		})

	s.Tool("tokenops_vendor_usage_status").
		Description("Return which usage sources are configured, whether each is switched on, and how many events each produced recently. Call it before trusting a spend or headroom figure: a source that is off reports nothing, and a source that is on but silent means ingestion has stopped. Each row carries the config hint naming what to set when a source is dark.").
		OutputSchema(vendorUsageResult{}).
		Handler(func(ctx context.Context, in vendorUsageInput) (*vendorUsageResult, error) {
			var count state.Counter
			if d.Counts != nil {
				count = d.Counts
			}
			res, err := state.VendorUsageOf(ctx, d.activeConfig(), count, in.WindowHours, time.Now())
			if err != nil {
				return nil, err
			}
			return &res, nil
		})
	return nil
}

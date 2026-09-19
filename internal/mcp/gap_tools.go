package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
)

// GapDeps wires the two surfaces that were CLI-only.
//
// Both answer questions an agent has and could not ask. `pricing` is what a
// model costs and whether the card has moved under it; `vendor-usage status`
// is whether the numbers it is about to quote are being fed at all.
// tokenops_data_sources reports event counts, which tells an agent that a
// source is silent but not whether it was ever switched on — the difference
// between "no data" and "no data because nothing is configured".
type GapDeps struct {
	// Config is the active configuration. Nil disables the vendor-usage
	// tool rather than reporting an empty one, because "no sources" and
	// "no config loaded" are different answers.
	Config *config.Config
	// ConfigGetter returns the live configuration at call time and takes
	// precedence over Config. tokenops_vendor_usage_setup switches sources
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

type pricingRate struct {
	Model       string  `json:"model"`
	InputPerM   float64 `json:"input_per_1m_usd"`
	OutputPerM  float64 `json:"output_per_1m_usd"`
	CacheReadPM float64 `json:"cache_read_per_1m_usd,omitempty"`
}

type pricingResult struct {
	Source    string        `json:"source"`
	FetchedAt string        `json:"fetched_at"`
	Models    int           `json:"models_in_snapshot"`
	Rates     []pricingRate `json:"rates"`
	Note      string        `json:"note,omitempty"`
}

type vendorUsageInput struct {
	WindowHours int `json:"window_hours,omitempty" jsonschema:"description=lookback in hours; default 24"`
}

type vendorUsageSourceStatus struct {
	Name        string `json:"name"`
	SourceTag   string `json:"source_tag"`
	Enabled     bool   `json:"enabled"`
	AlwaysOn    bool   `json:"always_on,omitempty"`
	EventsInWin int64  `json:"events_in_window"`
	ConfigHint  string `json:"config_hint,omitempty"`
}

type vendorUsageResult struct {
	WindowHours int                       `json:"window_hours"`
	Sources     []vendorUsageSourceStatus `json:"sources"`
	Error       string                    `json:"error,omitempty"`
	Hint        string                    `json:"hint,omitempty"`
}

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
			snap, ok := pricing.LatestSnapshot(d.PricingDir)
			if !ok {
				snap = pricing.BaselineSnapshot()
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 20
			}
			out := &pricingResult{
				Source:    snap.Source,
				FetchedAt: snap.FetchedAt.UTC().Format(time.RFC3339),
				Models:    len(snap.Rates),
			}
			keys := make([]string, 0, len(snap.Rates))
			for k := range snap.Rates {
				if in.Provider != "" && !strings.HasPrefix(k, strings.ToLower(in.Provider)+"/") {
					continue
				}
				if in.Model != "" && !strings.Contains(k, in.Model) {
					continue
				}
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(keys) > limit {
				out.Note = fmt.Sprintf("%d models matched; showing %d. Narrow with provider or model.", len(keys), limit)
				keys = keys[:limit]
			}
			for _, k := range keys {
				r := snap.Rates[k]
				out.Rates = append(out.Rates, pricingRate{
					Model:       k,
					InputPerM:   r.InputPerMillion,
					OutputPerM:  r.OutputPerMillion,
					CacheReadPM: r.CachedInputPerMillion,
				})
			}
			if len(out.Rates) == 0 {
				out.Note = "no model matched that filter"
			}
			return out, nil
		})

	s.Tool("tokenops_vendor_usage_status").
		Description("Return which usage sources are configured, whether each is switched on, and how many events each produced recently. Call it before trusting a spend or headroom figure: a source that is off reports nothing, and a source that is on but silent means ingestion has stopped. Each row carries the config hint naming what to set when a source is dark.").
		OutputSchema(vendorUsageResult{}).
		Handler(func(ctx context.Context, in vendorUsageInput) (*vendorUsageResult, error) {
			cfg := d.activeConfig()
			if cfg == nil || d.Counts == nil {
				return &vendorUsageResult{
					Error: "storage_disabled",
					Hint:  "run `tokenops init` then restart the daemon",
				}, nil
			}
			hours := in.WindowHours
			if hours <= 0 {
				hours = 24
			}
			now := time.Now()
			counts, err := d.Counts(ctx, now.Add(-time.Duration(hours)*time.Hour), now)
			if err != nil {
				return nil, err
			}
			res := &vendorUsageResult{WindowHours: hours}
			for _, src := range cfg.VendorUsageSources() {
				res.Sources = append(res.Sources, vendorUsageSourceStatus{
					Name:        src.Name,
					SourceTag:   src.SourceTag,
					Enabled:     src.Enabled || src.AlwaysOn,
					AlwaysOn:    src.AlwaysOn,
					EventsInWin: counts[src.SourceTag],
					ConfigHint:  cfg.VendorUsageConfigHint(src.SourceTag),
				})
			}
			return res, nil
		})
	return nil
}

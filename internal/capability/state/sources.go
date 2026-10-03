package state

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// Counter counts stored events per source over a range.
type Counter func(ctx context.Context, since, until time.Time) (map[string]int64, error)

// Errors in a payload's error field.
const ErrStorageDisabled = "storage_disabled"

// StorageDisabledHint says how to get an event store.
const StorageDisabledHint = "run `tokenops init` then restart the daemon"

// VendorUsageSource is one usage source: whether it is on and how much it
// produced.
type VendorUsageSource struct {
	Name        string `json:"name"`
	SourceTag   string `json:"source_tag"`
	Enabled     bool   `json:"enabled"`
	AlwaysOn    bool   `json:"always_on,omitempty"`
	EventsInWin int64  `json:"events_in_window"`
	ConfigHint  string `json:"config_hint,omitempty"`
}

// VendorUsage is every usage source over a window.
type VendorUsage struct {
	WindowHours int                 `json:"window_hours"`
	Sources     []VendorUsageSource `json:"sources"`
	Error       string              `json:"error,omitempty"`
	Hint        string              `json:"hint,omitempty"`
}

// VendorUsageOf reports which usage sources are on and how many events each
// produced in the last hours (24 when not positive). A source that is off
// reports nothing; one that is on but silent has stopped ingesting.
func VendorUsageOf(ctx context.Context, cfg *config.Config, count Counter, hours int, now time.Time) (VendorUsage, error) {
	if cfg == nil || count == nil {
		return VendorUsage{Error: ErrStorageDisabled, Hint: StorageDisabledHint}, nil
	}
	if hours <= 0 {
		hours = 24
	}
	counts, err := count(ctx, now.Add(-time.Duration(hours)*time.Hour), now)
	if err != nil {
		return VendorUsage{}, err
	}
	out := VendorUsage{WindowHours: hours}
	for _, src := range cfg.VendorUsageSources() {
		out.Sources = append(out.Sources, VendorUsageSource{
			Name:        src.Name,
			SourceTag:   src.SourceTag,
			Enabled:     src.Enabled || src.AlwaysOn,
			AlwaysOn:    src.AlwaysOn,
			EventsInWin: counts[src.SourceTag],
			ConfigHint:  cfg.VendorUsageConfigHint(src.SourceTag),
		})
	}
	return out, nil
}

// Window is the range a count covers, RFC 3339; empty is open.
type Window struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// DataSources is event counts per source plus each reader's health.
type DataSources struct {
	Counts map[string]int64 `json:"counts,omitempty"`
	Window *Window          `json:"window,omitempty"`
	// Sources is per-source ingestion health, when the daemon that runs
	// the readers can be asked.
	Sources   []freshness.Report `json:"sources,omitempty"`
	Unhealthy int                `json:"unhealthy,omitempty"`
	// FreshnessUnavailable explains a missing Sources.
	FreshnessUnavailable string `json:"freshness_unavailable,omitempty"`
	Error                string `json:"error,omitempty"`
	Hint                 string `json:"hint,omitempty"`
}

// DataSourcesOf counts events per source between since and until (an open
// until runs to now) and attaches each reader's health. health is nil when
// the readers' health cannot be asked.
func DataSourcesOf(ctx context.Context, count Counter, health []freshness.Report, since, until time.Time) (DataSources, error) {
	if count == nil {
		return DataSources{Error: ErrStorageDisabled, Hint: StorageDisabledHint}, nil
	}
	counts, err := count(ctx, since, until)
	if err != nil {
		return DataSources{}, err
	}
	out := DataSources{Counts: counts, Window: &Window{Since: rfc3339(since), Until: rfc3339(until)}, Sources: health}
	if health == nil {
		out.FreshnessUnavailable = "the ingestion daemon did not answer; counts are from the local store and say nothing about whether each reader is still working (" + config.DaemonRunRemedy + ")"
	}
	for _, r := range health {
		if !r.Healthy() {
			out.Unhealthy++
		}
	}
	return out, nil
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

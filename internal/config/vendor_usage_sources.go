package config

import (
	"context"
	"fmt"
	"time"
)

// VendorUsageSource pairs a vendor-usage source's display name with the
// SourceTag its poller stamps on every event it ingests, plus whether
// the config block is enabled. It is the single source of truth for the
// enabled-source↔tag mapping so the `tokenops vendor-usage status`
// command and the stale-ingestion health check can never drift.
type VendorUsageSource struct {
	// Name is the human-facing identifier (matches the config key).
	Name string
	// SourceTag is the value the poller writes into the event store's
	// source column; it is what CountBySource groups by.
	SourceTag string
	// Enabled mirrors the config block's enabled flag.
	Enabled bool
}

// VendorUsageSources returns every vendor-usage source in a stable
// order, each carrying its display name, the SourceTag its poller
// stamps on events, and whether the config block is enabled. Order is
// fixed so callers (and their tests) can rely on it.
func (c Config) VendorUsageSources() []VendorUsageSource {
	return []VendorUsageSource{
		{Name: "claude_code_jsonl", SourceTag: "claude-code-jsonl", Enabled: c.VendorUsage.ClaudeCodeJSONL.Enabled},
		{Name: "codex_jsonl", SourceTag: "codex-jsonl", Enabled: c.VendorUsage.CodexJSONL.Enabled},
		{Name: "opencode", SourceTag: "opencode", Enabled: c.VendorUsage.OpenCode.Enabled},
		{Name: "claude_code_stats_cache (deprecated)", SourceTag: "claude-code-stats-cache", Enabled: c.VendorUsage.ClaudeCode.Enabled},
		{Name: "vendor_usage_anthropic", SourceTag: "vendor-usage-anthropic", Enabled: c.VendorUsage.Anthropic.Enabled},
		{Name: "github_copilot", SourceTag: "github-copilot", Enabled: c.VendorUsage.GitHubCopilot.Enabled},
		{Name: "cursor_web", SourceTag: "cursor-web", Enabled: c.VendorUsage.Cursor.Enabled},
		{Name: "anthropic_cookie", SourceTag: "anthropic-cookie", Enabled: c.VendorUsage.AnthropicCookie.Enabled},
	}
}

// EnabledVendorUsageSources filters VendorUsageSources down to the
// sources whose config block is enabled, preserving order.
func (c Config) EnabledVendorUsageSources() []VendorUsageSource {
	all := c.VendorUsageSources()
	out := make([]VendorUsageSource, 0, len(all))
	for _, s := range all {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

// StaleIngestionWindow is the default lookback for the stale-ingestion
// health check. An enabled vendor-usage source that emitted zero events
// across this window is flagged as stale.
//
// 48h is deliberately generous: it tolerates a weekend of not touching a
// vendor while still catching a poller that silently died — the incident
// that motivated this check was a claude_code_jsonl poller that ingested
// nothing for ~a week while `tokenops status` still reported healthy.
const StaleIngestionWindow = 48 * time.Hour

// SourceCounter is the minimal slice of the event store the stale-
// ingestion check needs. *sqlite.Store satisfies it in production; tests
// pass a fake so the check is exercisable without a real database.
type SourceCounter interface {
	CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error)
}

// StaleSource names an enabled vendor-usage source that produced no
// events in the check window.
type StaleSource struct {
	Name        string `json:"name"`
	SourceTag   string `json:"source_tag"`
	WindowHours int    `json:"window_hours"`
}

// StaleIngestionNextAction is the remediation appended to next_actions
// whenever any vendor-usage source is stale.
const StaleIngestionNextAction = "check tokenops vendor-usage status; reconnect the MCP server or restart the daemon to resume ingestion"

// Warning renders the operator-facing warning line for a stale source.
// Kept here so the MCP status tool and the CLI status command emit
// byte-identical strings.
func (s StaleSource) Warning() string {
	return fmt.Sprintf(
		"ingestion stale: %s has 0 events in the last %dh — if you've been using it, reconnect/restart the poller (the MCP serve process may be a stale long-lived instance)",
		s.SourceTag, s.WindowHours)
}

// CheckStaleIngestion returns the enabled vendor-usage sources that
// emitted zero events between now-window and now.
//
// This is health/observability only. A zero count can legitimately mean
// "you simply have not used that vendor recently", so callers must
// surface the result as a soft warning — never a hard blocker. When
// window <= 0 the default StaleIngestionWindow is used. A nil counter or
// no enabled sources yields no warnings (never an error), keeping status
// non-panicking when the store is unavailable.
func (c Config) CheckStaleIngestion(ctx context.Context, counter SourceCounter, window time.Duration, now time.Time) ([]StaleSource, error) {
	enabled := c.EnabledVendorUsageSources()
	if len(enabled) == 0 || counter == nil {
		return nil, nil
	}
	if window <= 0 {
		window = StaleIngestionWindow
	}
	counts, err := counter.CountBySource(ctx, now.Add(-window), now)
	if err != nil {
		return nil, err
	}
	windowHours := int(window / time.Hour)
	var stale []StaleSource
	for _, s := range enabled {
		if counts[s.SourceTag] == 0 {
			stale = append(stale, StaleSource{
				Name:        s.Name,
				SourceTag:   s.SourceTag,
				WindowHours: windowHours,
			})
		}
	}
	return stale, nil
}

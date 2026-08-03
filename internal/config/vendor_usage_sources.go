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

// SourceLastSeen reports the most recent event per source. It is optional: a
// counter that does not implement it still yields warnings, just without the
// size of the gap. *sqlite.Store implements both.
type SourceLastSeen interface {
	LastEventBySource(ctx context.Context) (map[string]time.Time, error)
}

// StaleSource names an enabled vendor-usage source that produced no
// events in the check window.
type StaleSource struct {
	Name        string `json:"name"`
	SourceTag   string `json:"source_tag"`
	WindowHours int    `json:"window_hours"`

	// SilentFor is how long it has actually been since this source produced an
	// event. Zero means nothing was ever ingested from it, which is a
	// different fact from having stopped.
	//
	// Without this the warning could only say "0 events in the last 48h",
	// which reads the same on day two of an outage and on day twenty-seven. A
	// real incident ran 27 days while the text never changed, and the fixed
	// window made a month-long blackout look like a slow afternoon.
	SilentFor time.Duration `json:"silent_for,omitempty"`
}

// Staleness severity tiers. A warning that never escalates gets tuned out,
// which is how 27 days of no telemetry went unnoticed.
const (
	staleDegradedAfter = 7 * 24 * time.Hour
	staleCriticalAfter = 14 * 24 * time.Hour
)

// Severity grades the gap so a caller can escalate rather than repeat itself.
func (s StaleSource) Severity() string {
	switch {
	case s.SilentFor >= staleCriticalAfter:
		return "critical"
	case s.SilentFor >= staleDegradedAfter:
		return "degraded"
	default:
		return "warning"
	}
}

// silence renders how long the source has been quiet, in the units an operator
// would use.
func (s StaleSource) silence() string {
	if s.SilentFor <= 0 {
		return ""
	}
	if days := int(s.SilentFor.Hours() / 24); days >= 1 {
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("%dh", int(s.SilentFor.Hours()))
}

// StaleIngestionNextAction is the remediation appended to next_actions
// whenever any vendor-usage source is stale.
// The old text said to "reconnect the MCP server or restart the daemon",
// which conflates two different programs. During a 27-day outage eleven
// `tokenops serve` processes were running and the ingestion daemon was not;
// the advice pointed at the healthy half.
const StaleIngestionNextAction = "run 'tokenops vendor-usage status'; if a source is silent, start the ingestion daemon with 'tokenops start' ('tokenops serve' is the MCP server and does not ingest)"

// Warning renders the operator-facing warning line for a stale source.
// Kept here so the MCP status tool and the CLI status command emit
// byte-identical strings.
func (s StaleSource) Warning() string {
	const remedy = "if you've been using it, start the ingestion daemon ('tokenops start') — note that 'tokenops serve' is the MCP server and does not ingest"

	if s.SilentFor <= 0 {
		return fmt.Sprintf(
			"ingestion stale [%s]: %s is enabled but no events have ever been ingested from it — %s",
			s.Severity(), s.SourceTag, remedy)
	}
	return fmt.Sprintf(
		"ingestion stale [%s]: %s has produced no events for %s (checked a %dh window) — %s",
		s.Severity(), s.SourceTag, s.silence(), s.WindowHours, remedy)
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
	// How long each source has actually been quiet, when the store can say.
	// Best-effort: a counter without this still produces warnings, only
	// without the gap — which is the pre-existing behaviour, not a regression.
	var lastSeen map[string]time.Time
	if seer, ok := counter.(SourceLastSeen); ok {
		if seen, lErr := seer.LastEventBySource(ctx); lErr == nil {
			lastSeen = seen
		}
	}

	windowHours := int(window / time.Hour)
	var stale []StaleSource
	for _, s := range enabled {
		if counts[s.SourceTag] != 0 {
			continue
		}
		entry := StaleSource{
			Name:        s.Name,
			SourceTag:   s.SourceTag,
			WindowHours: windowHours,
		}
		if last, ok := lastSeen[s.SourceTag]; ok && !last.IsZero() && now.After(last) {
			entry.SilentFor = now.Sub(last)
		}
		stale = append(stale, entry)
	}
	return stale, nil
}

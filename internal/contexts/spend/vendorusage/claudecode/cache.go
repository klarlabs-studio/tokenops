// Package claudecode reads the Claude Code CLI's local stats cache
// (~/.claude/stats-cache.json) and emits PromptEvent envelopes for the
// activity it observes. The cache is intentionally undocumented by
// Anthropic — schema may shift between Claude Code releases — so the
// reader is defensive: unknown fields are ignored, parse failures are
// surfaced to the caller with the path included so operators know
// where to look.
//
// Why bother reading an undocumented file?
//
//   - Max-plan subscribers have no documented server-side usage
//     endpoint (confirmed via the Anthropic Admin API surface). The
//     stats cache is the only locally-available signal that reflects
//     real Claude Code activity counts and per-model token totals.
//   - The reader sits behind a clear signal_quality caveat: medium
//     confidence, "reads undocumented Claude Code internal cache,
//     daily granularity only". TokenOps never claims this is a quota
//     meter — it's the best activity proxy currently available.
//
// The cache aggregates *daily* counts; it carries no 5-hour rolling
// window. Headroom math for the live window still depends on MCP
// session pings or proxy traffic. What we gain here is reliable
// per-model attribution + accurate today-vs-yesterday burn.
package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StatsCache is the subset of the Claude Code cache TokenOps reads.
// Unknown fields in the JSON are ignored by encoding/json so the
// reader stays forward-compatible if Anthropic adds keys; we only
// break when an existing key is renamed or its type changes.
type StatsCache struct {
	Version          int                `json:"version"`
	LastComputedDate string             `json:"lastComputedDate"`
	DailyActivity    []DailyActivity    `json:"dailyActivity"`
	DailyModelTokens []DailyModelTokens `json:"dailyModelTokens"`
	ModelUsage       map[string]Model   `json:"modelUsage"`
	TotalSessions    int                `json:"totalSessions"`
	TotalMessages    int64              `json:"totalMessages"`
	FirstSessionDate time.Time          `json:"firstSessionDate"`
}

// DailyActivity is one row from the cache's per-day message log. The
// date is the local-time "YYYY-MM-DD" string Claude Code writes; we
// don't try to be clever about timezones because the cache offers no
// hint about which zone the strings live in (observed: machine-local).
type DailyActivity struct {
	Date          string `json:"date"`
	MessageCount  int64  `json:"messageCount"`
	SessionCount  int    `json:"sessionCount"`
	ToolCallCount int64  `json:"toolCallCount"`
}

// DailyModelTokens carries the per-day token total broken down by
// model name. Claude Code writes the full model snapshot id
// ("claude-opus-4-7", "claude-haiku-4-5-20251001", etc.) so we can
// route into the pricing table directly without normalisation.
type DailyModelTokens struct {
	Date          string           `json:"date"`
	TokensByModel map[string]int64 `json:"tokensByModel"`
}

// Model mirrors the cumulative per-model summary at the bottom of the
// cache. CostUSD is always zero in observed caches (Claude Code does
// not compute cost client-side); we ignore it and rely on TokenOps's
// spend.Engine to attach a price.
type Model struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
	WebSearchRequests        int   `json:"webSearchRequests"`
}

// DefaultPath returns the conventional cache location. Operators can
// override (e.g. when Claude Code's home is symlinked or relocated via
// CLAUDE_HOME). Empty home → an error so the caller surfaces a clear
// hint instead of silently reading from "/stats-cache.json".
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "stats-cache.json"), nil
}

// Read parses the cache at path. Returns os.ErrNotExist when the file
// is absent so callers can branch on "Claude Code not installed yet"
// vs. a corrupted cache. JSON parse errors carry the path so logs
// point at the offending file.
func Read(path string) (*StatsCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c StatsCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}

// ActivityForDate finds the DailyActivity row matching the YYYY-MM-DD
// key. Returns false when the day has no row (Claude Code only writes
// days with non-zero traffic). Linear scan is fine — the cache caps
// out at a few hundred days even for heavy users.
func (c *StatsCache) ActivityForDate(date string) (DailyActivity, bool) {
	for _, row := range c.DailyActivity {
		if row.Date == date {
			return row, true
		}
	}
	return DailyActivity{}, false
}

// TokensForDate returns the tokens-by-model map for date or nil when
// the day isn't present. Returns the live map reference; callers must
// not mutate.
func (c *StatsCache) TokensForDate(date string) map[string]int64 {
	for _, row := range c.DailyModelTokens {
		if row.Date == date {
			return row.TokensByModel
		}
	}
	return nil
}

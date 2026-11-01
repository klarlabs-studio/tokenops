// Package claudecodeorg reads an organisation's Claude Code use as
// Anthropic reports it, with a Claude Console admin key: sessions, lines,
// commits, pull requests, accepted and rejected edits, tokens and
// Anthropic's estimated cost, per UTC day, summed over the organisation's
// users (usage.ClaudeCodeReporter). It covers Claude Code billed through
// the Console organisation; sign-ins with a Claude subscription are
// reported under claude.ai and are not in it.
package claudecodeorg

import (
	"context"
	"fmt"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
	anthropicapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/anthropic"
)

// MaxDays bounds one report: each day is one or more requests.
const MaxDays = 90

// Scope says what the report covers, for every surface that shows it.
const Scope = "Claude Code billed through the Claude Console organisation of the admin key; sign-ins with a Claude subscription (Pro, Max, Team, Enterprise) are not included"

// Day is one UTC day of the organisation's Claude Code use.
type Day = usage.ClaudeCodeDay

// ToolDecisions and Model are a day's per-tool and per-model figures.
type (
	ToolDecisions = usage.ToolDecisions
	Model         = usage.ClaudeCodeModel
)

// Report is the organisation's Claude Code use over a range of UTC days.
type Report struct {
	Since string `json:"since"`
	Until string `json:"until"`
	Scope string `json:"scope"`
	// Days are oldest first, one per UTC day in the range.
	Days  []usage.ClaudeCodeDay `json:"days"`
	Total usage.ClaudeCodeDay   `json:"total"`
}

// Reporter is the Admin API client for adminKey.
func Reporter(adminKey string) usage.ClaudeCodeReporter { return anthropicapi.NewAdminClient(adminKey) }

// Read reports the days days up to and including until's UTC day.
func Read(ctx context.Context, r usage.ClaudeCodeReporter, until time.Time, days int) (Report, error) {
	if days < 1 || days > MaxDays {
		return Report{}, fmt.Errorf("days: 1 to %d", MaxDays)
	}
	last := until.UTC().Truncate(24 * time.Hour)
	first := last.AddDate(0, 0, -(days - 1))
	rep := Report{Since: first.Format("2006-01-02"), Until: last.Format("2006-01-02"), Scope: Scope}
	rep.Total.Day = rep.Since + ".." + rep.Until
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		day, err := r.ClaudeCodeDay(ctx, d)
		if err != nil {
			return Report{}, fmt.Errorf("claude code analytics for %s: %w", d.Format("2006-01-02"), err)
		}
		rep.Days = append(rep.Days, day)
		rep.Total.Add(day)
	}
	return rep, nil
}

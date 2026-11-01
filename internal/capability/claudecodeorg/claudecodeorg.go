// Package claudecodeorg reads an organisation's Claude Code use as
// Anthropic reports it, per UTC day, summed over the organisation's
// users: sessions, lines, commits, pull requests, accepted and rejected
// edits and, where the report has them, tokens and Anthropic's estimated
// cost (usage.ClaudeCodeReporter). Anthropic reports it in two places:
//
//   - the Claude Code Analytics API, with a Claude Console admin key:
//     Claude Code billed through the Console organisation, with tokens
//     and estimated cost by model;
//   - the Claude Enterprise Analytics API, with an Analytics API key:
//     Claude Code by the organisation's claude.ai sign-ins, without cost,
//     about a day behind.
//
// Pro, Max and Team subscriptions have neither; TokenOps reads those from
// each machine.
package claudecodeorg

import (
	"context"
	"errors"
	"fmt"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
	anthropicapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/anthropic"
)

// MaxDays bounds one report: each day is one or more requests.
const MaxDays = 90

// Day is one UTC day of the organisation's Claude Code use.
type Day = usage.ClaudeCodeDay

// ToolDecisions and Model are a day's per-tool and per-model figures.
type (
	ToolDecisions = usage.ToolDecisions
	Model         = usage.ClaudeCodeModel
)

// The sources, by the key that reads them.
const (
	SourceConsole    = "console"
	SourceEnterprise = "enterprise"
)

// Source is one of Anthropic's reports and the client that reads it.
type Source struct {
	Name     string
	Scope    string
	Reporter usage.ClaudeCodeReporter
}

// Sources are the reports the given keys can read, Console first.
func Sources(adminKey, analyticsKey string) []Source {
	var out []Source
	if adminKey != "" {
		out = append(out, Source{Name: SourceConsole, Reporter: anthropicapi.NewAdminClient(adminKey),
			Scope: "Claude Code billed through the Claude Console organisation of the admin key, with Anthropic's estimated cost"})
	}
	if analyticsKey != "" {
		out = append(out, Source{Name: SourceEnterprise, Reporter: &anthropicapi.AnalyticsClient{Key: analyticsKey},
			Scope: "Claude Code by the Claude Enterprise organisation's claude.ai sign-ins; no cost in this report, and about a day behind"})
	}
	return out
}

// ErrNoKey says neither key is configured.
var ErrNoKey = errors.New("no Anthropic admin key or Claude Enterprise Analytics API key: set vendor_usage.anthropic.admin_key (TOKENOPS_ANTHROPIC_ADMIN_KEY, a Claude Console admin key) or vendor_usage.anthropic.analytics_key (TOKENOPS_ANTHROPIC_ANALYTICS_KEY, a Claude Enterprise Analytics API key)")

// Report is one source's Claude Code use over a range of UTC days.
type Report struct {
	Source string `json:"source"`
	Scope  string `json:"scope"`
	Since  string `json:"since"`
	Until  string `json:"until"`
	// Days are oldest first, one per published UTC day in the range;
	// Pending names the days the source has not published yet.
	Days    []Day    `json:"days"`
	Pending []string `json:"pending,omitempty"`
	Total   Day      `json:"total"`
}

// Result is every configured source's report.
type Result struct {
	Reports []Report `json:"reports"`
}

// ReadAll reads every source in turn.
func ReadAll(ctx context.Context, sources []Source, until time.Time, days int) (Result, error) {
	if len(sources) == 0 {
		return Result{}, ErrNoKey
	}
	var res Result
	for _, s := range sources {
		r, err := Read(ctx, s, until, days)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", s.Name, err)
		}
		res.Reports = append(res.Reports, r)
	}
	return res, nil
}

// Read reports the days days up to and including until's UTC day.
func Read(ctx context.Context, s Source, until time.Time, days int) (Report, error) {
	if days < 1 || days > MaxDays {
		return Report{}, fmt.Errorf("days: 1 to %d", MaxDays)
	}
	last := until.UTC().Truncate(24 * time.Hour)
	first := last.AddDate(0, 0, -(days - 1))
	rep := Report{Source: s.Name, Scope: s.Scope, Since: first.Format("2006-01-02"), Until: last.Format("2006-01-02")}
	rep.Total.Day = rep.Since + ".." + rep.Until
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		day, err := s.Reporter.ClaudeCodeDay(ctx, d)
		if errors.Is(err, usage.ErrDayNotAvailable) {
			rep.Pending = append(rep.Pending, d.Format("2006-01-02"))
			continue
		}
		if err != nil {
			return Report{}, fmt.Errorf("claude code analytics for %s: %w", d.Format("2006-01-02"), err)
		}
		rep.Days = append(rep.Days, day)
		rep.Total.Add(day)
	}
	return rep, nil
}

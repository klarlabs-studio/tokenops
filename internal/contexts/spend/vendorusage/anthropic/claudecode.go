package anthropic

import (
	"context"
	"time"
)

// ClaudeCodeReporter reads the Admin API's Claude Code analytics
// (GET /v1/organizations/usage_report/claude_code): one UTC day of the
// organisation's Claude Code use, summed over its users. The HTTP client
// in internal/infra/vendorusage/anthropic satisfies it.
//
// The report covers Claude Code authenticated against the Claude Console
// organisation the key belongs to. Sign-ins with a Claude subscription
// (Pro, Max, Team, Enterprise) are counted under claude.ai and are not in
// it.
type ClaudeCodeReporter interface {
	ClaudeCodeDay(ctx context.Context, day time.Time) (ClaudeCodeDay, error)
}

// ClaudeCodeDay is the organisation's Claude Code use on one UTC day, as
// Anthropic reports it. The report is per user; the reader sums it and
// keeps no one's identity, only how many users and API keys there were.
type ClaudeCodeDay struct {
	// Day is the UTC day, "2006-01-02".
	Day string `json:"day"`
	// Users counts users signed in through the Console (user actors);
	// APIKeys counts API keys used directly (API actors).
	Users   int `json:"users"`
	APIKeys int `json:"api_keys"`

	Sessions     int64 `json:"sessions"`
	LinesAdded   int64 `json:"lines_added"`
	LinesRemoved int64 `json:"lines_removed"`
	Commits      int64 `json:"commits"`
	PullRequests int64 `json:"pull_requests"`
	// Tools is each editing tool's accepted and rejected proposals,
	// keyed as the report names them (edit_tool, multi_edit_tool,
	// write_tool, notebook_edit_tool).
	Tools map[string]ToolDecisions `json:"tools,omitempty"`
	// Models is each model's tokens and Anthropic's estimated cost.
	Models map[string]ClaudeCodeModel `json:"models,omitempty"`
}

// ToolDecisions counts a tool's proposals the user accepted or rejected.
type ToolDecisions struct {
	Accepted int64 `json:"accepted"`
	Rejected int64 `json:"rejected"`
}

// ClaudeCodeModel is one model's tokens and estimated cost on a day.
type ClaudeCodeModel struct {
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	EstimatedUSD        float64 `json:"estimated_usd"`
}

// Decisions sums every tool's accepted and rejected proposals.
func (d ClaudeCodeDay) Decisions() ToolDecisions {
	var t ToolDecisions
	for _, v := range d.Tools {
		t.Accepted += v.Accepted
		t.Rejected += v.Rejected
	}
	return t
}

// AcceptancePct is the share of proposals accepted, 0 to 100; false with
// no proposals.
func (t ToolDecisions) AcceptancePct() (float64, bool) {
	n := t.Accepted + t.Rejected
	if n == 0 {
		return 0, false
	}
	return float64(t.Accepted) / float64(n) * 100, true
}

// EstimatedUSD is Anthropic's estimated cost over every model.
func (d ClaudeCodeDay) EstimatedUSD() float64 {
	var usd float64
	for _, m := range d.Models {
		usd += m.EstimatedUSD
	}
	return usd
}

// Tokens is every model's tokens, cache included.
func (d ClaudeCodeDay) Tokens() int64 {
	var n int64
	for _, m := range d.Models {
		n += m.InputTokens + m.OutputTokens + m.CacheReadTokens + m.CacheCreationTokens
	}
	return n
}

// Add sums o into d (Users and APIKeys add too: a sum over days counts
// user-days).
func (d *ClaudeCodeDay) Add(o ClaudeCodeDay) {
	d.Users += o.Users
	d.APIKeys += o.APIKeys
	d.Sessions += o.Sessions
	d.LinesAdded += o.LinesAdded
	d.LinesRemoved += o.LinesRemoved
	d.Commits += o.Commits
	d.PullRequests += o.PullRequests
	for k, v := range o.Tools {
		if d.Tools == nil {
			d.Tools = map[string]ToolDecisions{}
		}
		t := d.Tools[k]
		t.Accepted += v.Accepted
		t.Rejected += v.Rejected
		d.Tools[k] = t
	}
	for k, v := range o.Models {
		if d.Models == nil {
			d.Models = map[string]ClaudeCodeModel{}
		}
		m := d.Models[k]
		m.InputTokens += v.InputTokens
		m.OutputTokens += v.OutputTokens
		m.CacheReadTokens += v.CacheReadTokens
		m.CacheCreationTokens += v.CacheCreationTokens
		m.EstimatedUSD += v.EstimatedUSD
		d.Models[k] = m
	}
}

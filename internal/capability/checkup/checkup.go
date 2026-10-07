// Package checkup is the one-shot look at the last week of agent work:
// what it cost, where it leaked and how it felt, with the command that
// fixes each leak. It reads the clients' own transcripts directly, so it
// works on a machine with no config, no daemon and no event store: the
// first thing someone runs, not the last.
//
// Prompt text is read into memory to classify instructions and is never
// written anywhere, as everywhere else in TokenOps.
package checkup

import (
	"context"
	"sort"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// Options say where to read and how far back.
type Options struct {
	// Home is the operator's home directory, where every client keeps its
	// records.
	Home string
	// Days is the window; zero means 7.
	Days int
	Now  time.Time
	// Installed names the TokenOps hooks already wired into Claude Code
	// ("read-guard", "route-guard", "coach-hook"), so a finding does not
	// tell the operator to install what is on.
	Installed map[string]bool
}

// Level orders findings.
type Level string

// Finding levels.
const (
	LevelWarn   Level = "warn"
	LevelNotice Level = "notice"
	LevelInfo   Level = "info"
)

// Finding is one leak, with the evidence and the command that fixes it.
type Finding struct {
	Kind     string `json:"kind"`
	Level    Level  `json:"level"`
	Title    string `json:"title"`
	Evidence string `json:"evidence"`
	// Fix is the TokenOps command that acts on it, run once.
	Fix string `json:"fix"`
	// CostUSD is what it cost in the window at API prices, when known.
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// Report is the checkup.
type Report struct {
	Window   string      `json:"window"`
	Usage    []Usage     `json:"usage"`
	Total    Usage       `json:"total"`
	DX       sessions.DX `json:"dx"`
	Findings []Finding   `json:"findings"`
	// Warnings names what could not be read; the rest is still reported.
	Warnings []string `json:"warnings,omitempty"`
	// Partial means the usage read stopped before every source was read,
	// so Usage and Total undercount.
	Partial bool `json:"partial,omitempty"`
}

// Compute runs the checkup.
func Compute(ctx context.Context, o Options) Report {
	if o.Days <= 0 {
		o.Days = 7
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	since := o.Now.AddDate(0, 0, -o.Days)
	window := "last " + strconv.Itoa(o.Days) + "d"

	read := readUsage(ctx, o.Home, since)
	turnsByCWD := read.turnsByCWD
	records, err := agentdx.ExtractAll(agentdx.ExtractOptions{Since: since, WithPromptText: true})
	r := Report{
		Window:   window,
		Usage:    read.usage,
		Total:    total(read.usage),
		DX:       sessions.DXFromRecords(records, err, window),
		Warnings: read.warnings,
		Partial:  read.partial,
	}
	r.Findings = append(r.Findings, rereads(records, o.Installed["read-guard"])...)
	r.Findings = append(r.Findings, standingContext(o.Home, turnsByCWD, r.Total)...)
	r.Findings = append(r.Findings, oversized(records, o.Installed["route-guard"])...)
	if f, ok := dxFinding(r.DX); ok {
		r.Findings = append(r.Findings, f)
	}
	rank := map[Level]int{LevelWarn: 0, LevelNotice: 1, LevelInfo: 2}
	sort.SliceStable(r.Findings, func(i, j int) bool { return rank[r.Findings[i].Level] < rank[r.Findings[j].Level] })
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	return r
}

// dxFinding carries the DX recommendation as a finding: it is the single
// change the grades point at.
func dxFinding(dx sessions.DX) (Finding, bool) {
	rec := dx.Recommendation
	if rec == nil {
		return Finding{}, false
	}
	return Finding{
		Kind: "dx", Level: LevelNotice, Title: rec.Title, Evidence: rec.Evidence,
		Fix: "tokenops init   (installs the coach, which raises this as it happens)",
	}, true
}

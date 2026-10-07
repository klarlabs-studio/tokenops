package spending

import (
	"context"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/tools"
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard"
	opencodestore "go.klarlabs.de/tokenops/internal/infra/opencodedb"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// ScorecardParams selects the scorecard's window and operator overrides.
type ScorecardParams struct {
	// SinceDays is the window; seven when zero.
	SinceDays int
	// The overrides replace a measured KPI with the operator's figure.
	FVTSeconds, TEUPct, SACPct float64
	BaselineRef                string
	// TranscriptRoot is where the agent KPIs read session transcripts;
	// empty means the harnesses' default locations.
	TranscriptRoot string
	// Now anchors the window; the current time when zero.
	Now time.Time
}

// ScorecardReport is the scorecard answer, named here so adapters can
// describe it without reaching into the governance domain.
type ScorecardReport = scorecard.Scorecard

// Scorecard grades the wedge KPIs (FVT, TEU, SAC, CHR) from the store and
// the agent KPIs (CGR, RGR, TCS, DAR) from session transcripts. A nil
// store means there is no event history yet.
//
// The CLI used to be the only surface that added the agent KPIs, so the
// MCP tool and /api/scorecard graded the same week on four fewer
// metrics. Every surface now asks this function.
func Scorecard(ctx context.Context, store *sqlite.Store, p ScorecardParams) *ScorecardReport {
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	days := p.SinceDays
	if days <= 0 {
		days = 7
	}
	var reader scorecard.EventReader
	if store != nil {
		reader = store
	}
	return scorecard.Build(ctx, reader, scorecard.BuildParams{
		SinceDays:          days,
		FVTSecondsOverride: p.FVTSeconds,
		TEUPctOverride:     p.TEUPct,
		SACPctOverride:     p.SACPct,
		AgentKPIs:          agentKPIs(p.TranscriptRoot, now.Add(-time.Duration(days)*24*time.Hour)),
		BaselineRef:        p.BaselineRef,
		ClockNow:           func() time.Time { return now },
	})
}

// agentKPIs derives the KPIs the event store cannot: CGR and RGR need
// prompt text, TCS and DAR need tool results. It returns a zero value,
// with no *Computed flag set, when there are no prompts since then.
func agentKPIs(root string, since time.Time) scorecard.AgentKPIInputs {
	extracted, err := prompts.Extract(prompts.ExtractOptions{Root: root, Since: since, Opencode: opencodestore.Store{}})
	if err != nil || len(extracted) == 0 {
		return scorecard.AgentKPIInputs{}
	}
	f := prompts.Analyze(withoutLoopSentinels(extracted))
	if f.TotalPrompts == 0 {
		return scorecard.AgentKPIInputs{}
	}
	out := scorecard.AgentKPIInputs{
		ConfirmationGateRatePct:  100.0 * float64(f.Acknowledgements) / float64(f.TotalPrompts),
		ConfirmationGateComputed: true,
		RegenerateRatePct:        100.0 * float64(f.Regenerates) / float64(f.TotalPrompts),
		RegenerateComputed:       true,
	}
	if toolEvs, err := tools.Extract(tools.ExtractOptions{Root: root, Since: since}); err == nil && len(toolEvs) > 0 {
		ts := tools.Analyze(toolEvs)
		if ts.TotalToolCalls > 0 {
			out.ToolSuccessRatePct = ts.SuccessRate
			out.ToolSuccessComputed = true
			out.DestructiveRatePct = ts.DestructiveRate
			out.DestructiveComputed = true
		}
	}
	return out
}

// loopSentinelRepeats is the gap between "I typed continue twice" (real
// steering) and an autonomous /loop that fired dozens of times.
const loopSentinelRepeats = 5

// withoutLoopSentinels drops `continue` / `proceed` / `keep going`
// prompts that repeat more than loopSentinelRepeats times in one session.
// Those are /loop pacing sentinels (the harness waking the agent), not
// human acknowledgements, and counting them in CGR penalises autonomous
// workflows.
func withoutLoopSentinels(in []prompts.UserPrompt) []prompts.UserPrompt {
	type key struct{ session, text string }
	sentinel := map[string]bool{"continue": true, "proceed": true, "keep going": true}
	counts := map[key]int{}
	for _, p := range in {
		if lc := strings.ToLower(strings.TrimSpace(p.Text)); sentinel[lc] {
			counts[key{p.SessionID, lc}]++
		}
	}
	out := make([]prompts.UserPrompt, 0, len(in))
	for _, p := range in {
		lc := strings.ToLower(strings.TrimSpace(p.Text))
		if sentinel[lc] && counts[key{p.SessionID, lc}] > loopSentinelRepeats {
			continue
		}
		out = append(out, p)
	}
	return out
}

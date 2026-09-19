package plans

import (
	"context"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SpendLimit is the operator-supplied limit for a spend-denominated plan
// (config plan_limits). It is the fallback when the vendor does not report
// the limit itself.
type SpendLimit struct {
	LimitUSD   float64
	Window     string
	RateFactor float64
}

// SourceCounter counts events per source over a range. It grades how far a
// headroom report can be trusted; nil reports the lowest grade.
type SourceCounter func(ctx context.Context, since, until time.Time) (map[string]int64, error)

// SignalFromCounts maps per-source event counts onto the observations
// ClassifySignal grades.
func SignalFromCounts(counts map[string]int64) SignalInputs {
	return SignalInputs{
		ProxyEventsInWindow:      counts["proxy"],
		MCPPingsInWindow:         counts["mcp-session"],
		ClaudeCodeCacheInWindow:  counts["claude-code-stats-cache"],
		ClaudeCodeJSONLInWindow:  counts["claude-code-jsonl"],
		CodexJSONLInWindow:       counts["codex-jsonl"],
		CopilotInWindow:          counts["github-copilot"],
		CursorInWindow:           counts["cursor-web"],
		ClaudeUsageMeterInWindow: counts["claude-usage-meter"],
		VendorAPIWired:           counts["vendor-usage-anthropic"] > 0,
	}
}

// AssembleHeadroomInputs gathers everything ComputeHeadroom reads for one
// provider's plan.
//
// The terminal and the MCP tool used to assemble these separately, and
// each dropped something the other had: `plan headroom` never read the
// vendor's own window %, and `tokenops_plan_headroom` never read the
// Enterprise spend limit — so an agent was told no limit was configured
// when one was. One assembly keeps them answering the same question.
func AssembleHeadroomInputs(ctx context.Context, reader EventReader, counts SourceCounter, provider, planName string, lim SpendLimit, now time.Time) (HeadroomInputs, error) {
	cons, err := ConsumptionFor(ctx, reader, provider, now)
	if err != nil {
		return HeadroomInputs{}, fmt.Errorf("consumption[%s]: %w", provider, err)
	}
	in := HeadroomInputs{
		ConsumedTokens:       cons.ConsumedTokens,
		Last7DayTokens:       cons.Last7DayTokens,
		Now:                  now,
		MonthlyAuthoritative: LatestAuthoritativeMonthly(ctx, reader, providerOf(provider), now),
	}
	p, ok := Lookup(planName)
	if !ok {
		return in, nil
	}
	if p.RateLimitWindow > 0 {
		win, err := ConsumptionInWindow(ctx, reader, provider, now, p.RateLimitWindow)
		if err != nil {
			return HeadroomInputs{}, fmt.Errorf("window[%s]: %w", provider, err)
		}
		in.WindowMessages = win.MessagesInWindow
		if counts != nil {
			c, err := counts(ctx, now.Add(-p.RateLimitWindow), now)
			if err != nil {
				return HeadroomInputs{}, fmt.Errorf("signal[%s]: %w", provider, err)
			}
			in.Signal = SignalFromCounts(c)
		}
		in.Authoritative = LatestAuthoritativeWindow(ctx, reader, providerOf(provider), p, now)
	}
	if p.SpendDenominated {
		in.SpendLimitUSD = lim.LimitUSD
		in.RateFactor = lim.RateFactor
		spend, err := SpendInWindow(ctx, reader, provider, now, lim.Window)
		if err != nil {
			return HeadroomInputs{}, fmt.Errorf("spend[%s]: %w", provider, err)
		}
		in.SpendUSD = spend
		in.VendorSpend = LatestVendorSpend(ctx, reader, providerOf(provider), now)
	}
	return in, nil
}

func providerOf(provider string) eventschema.Provider { return eventschema.Provider(provider) }

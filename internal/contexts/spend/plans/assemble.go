package plans

import (
	"context"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SpendLimit is the operator-supplied limit for a spend-denominated plan
// (config plan_limits). It is the fallback when the vendor does not report
// the limit itself.
type SpendLimit struct {
	LimitUSD   float64
	Window     string
	RateFactor float64
	// Price prices a request at list rates. Stored events carry no cost
	// for usage priced at read time, so a spend total without it counts
	// only what the proxy measured. nil keeps that behaviour.
	Price Pricer
}

// Pricer prices a request at the rate card in force at a moment.
type Pricer func(p *eventschema.PromptEvent, at time.Time) (float64, error)

// SourceCounter counts events per source over a range. It grades how far a
// headroom report can be trusted; nil reports the lowest grade.
type SourceCounter func(ctx context.Context, since, until time.Time) (map[string]int64, error)

// sourceProvider names the provider a vendor-usage source reports on, from
// the provider registry. Sources absent here (the proxy, MCP session pings,
// opencode's store) carry traffic for any provider and count for all of
// them.
var sourceProvider = providers.SourceProviders()

// accountSources are the vendor account and gateway readers' tags: their
// readings all grade as a vendor's own account figures.
var accountSources = func() []string {
	var out []string
	for _, s := range providers.Sources() {
		if s.Switch == providers.SwitchAccounts {
			out = append(out, s.Tag)
		}
	}
	return out
}()

// SignalFromCounts maps per-source event counts onto the observations
// ClassifySignal grades, keeping only sources that report on provider.
//
// Counting every source for every provider graded a Codex plan by Claude
// Code's transcripts: headroom for an OpenAI subscription carried "Reads
// ~/.claude/projects" as its signal, and its confidence came from data
// that says nothing about Codex. An empty provider keeps every source.
func SignalFromCounts(counts map[string]int64, provider string) SignalInputs {
	c := func(source string) int64 {
		if owner, ok := sourceProvider[source]; ok && provider != "" && owner != provider {
			return 0
		}
		return counts[source]
	}
	return SignalInputs{
		ProxyEventsInWindow:      c("proxy"),
		MCPPingsInWindow:         c("mcp-session"),
		ClaudeCodeCacheInWindow:  c("claude-code-stats-cache"),
		ClaudeCodeJSONLInWindow:  c("claude-code-jsonl"),
		CodexJSONLInWindow:       c("codex-jsonl"),
		CopilotInWindow:          c("github-copilot"),
		CursorInWindow:           c("cursor-web"),
		ClaudeUsageMeterInWindow: c("claude-usage-meter"),
		ClaudeStatuslineInWindow: c("claude-code-statusline"),
		FireworksInWindow:        c("fireworks-usage"),
		VendorAccountInWindow:    sum(c, accountSources),
		VendorAPIWired:           c("vendor-usage-anthropic") > 0,
	}
}

// sum adds count over tags.
func sum(count func(string) int64, tags []string) int64 {
	var n int64
	for _, t := range tags {
		n += count(t)
	}
	return n
}

// AssembleHeadroomInputs gathers everything ComputeHeadroom reads for one
// provider's plan.
//
// The terminal and the MCP tool used to assemble these separately, and
// each dropped something the other had: `plan headroom` never read the
// vendor's own window %, and `tokenops_glance (view=headroom)` never read the
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
		in.WindowStartedAt = win.FirstActivityAt
		if counts != nil {
			c, err := counts(ctx, now.Add(-p.RateLimitWindow), now)
			if err != nil {
				return HeadroomInputs{}, fmt.Errorf("signal[%s]: %w", provider, err)
			}
			in.Signal = SignalFromCounts(c, provider)
		}
		in.Authoritative = LatestAuthoritativeWindow(ctx, reader, providerOf(provider), p, now)
	}
	// Whatever the plan, the vendor's own windows are shown when it
	// reports them: a subscription the catalog does not know, a spend
	// limit behind a gateway.
	in.VendorWindows = VendorWindows(ctx, reader, providerOf(provider), now)
	if p.SpendDenominated {
		in.SpendLimitUSD = lim.LimitUSD
		in.RateFactor = lim.RateFactor
		spendIn := SpendInWindow
		if planName == PayAsYouGo {
			spendIn = BilledSpendInWindow
		}
		spend, err := spendIn(ctx, reader, provider, now, lim.Window, lim.Price)
		if err != nil {
			return HeadroomInputs{}, fmt.Errorf("spend[%s]: %w", provider, err)
		}
		in.SpendUSD = spend
		in.VendorSpend = LatestVendorSpend(ctx, reader, providerOf(provider), now)
		// Grade the reading by what fed it over the spend window. It was
		// only graded for window plans, so a spend plan read from Claude
		// Code's own logs was labelled the lowest grade.
		if counts != nil {
			c, err := counts(ctx, SpendWindowStart(now, lim.Window), now)
			if err != nil {
				return HeadroomInputs{}, fmt.Errorf("signal[%s]: %w", provider, err)
			}
			in.Signal = SignalFromCounts(c, provider)
		}
	}
	return in, nil
}

func providerOf(provider string) eventschema.Provider { return eventschema.Provider(provider) }

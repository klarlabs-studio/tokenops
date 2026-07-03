package mcp

import (
	"context"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// latestAuthoritativeWindow scans recent events for the newest vendor
// quota snapshot matching provider + window and converts it into an
// authoritative override for ComputeSessionBudget. The vendor pollers
// already store their own reported "% of limit used" (+ reset time) in
// event attributes; this is where the prediction finally reads them
// instead of extrapolating from a message count. Returns nil when no
// snapshot is available, so callers fall back to the heuristic.
func latestAuthoritativeWindow(ctx context.Context, reader plans.EventReader, provider eventschema.Provider, p plans.Plan, now time.Time) *plans.AuthoritativeWindow {
	weekly := p.RateLimitWindow > 24*time.Hour
	usedKey, resetKey, isRemaining, source := authoritativeKeys(provider, weekly)
	if usedKey == "" {
		return nil
	}
	lookback := 2 * p.RateLimitWindow
	if lookback < time.Hour {
		lookback = time.Hour
	}
	events, err := reader.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-lookback))
	if err != nil {
		return nil
	}

	var best *eventschema.Envelope
	for _, e := range events {
		if e == nil || e.Attributes == nil {
			continue
		}
		if _, ok := e.Attributes[usedKey]; !ok {
			continue
		}
		if best == nil || e.Timestamp.After(best.Timestamp) {
			best = e
		}
	}
	if best == nil {
		return nil
	}
	pct, err := strconv.ParseFloat(best.Attributes[usedKey], 64)
	if err != nil {
		return nil
	}
	if isRemaining {
		pct = 100 - pct
	}
	return &plans.AuthoritativeWindow{
		UsedPct:  pct,
		ResetsIn: parseResetsIn(best.Attributes[resetKey], now),
		Source:   source,
	}
}

// authoritativeKeys maps a provider + window kind to the attribute keys
// its poller writes. The used-% keys are provider-unique (five_hour_*
// for the Anthropic cookie, primary_* for Codex rate_limits,
// percent_remaining for Copilot), so matching on the key alone already
// isolates the right source. isRemaining flags Copilot, which reports
// remaining rather than used.
func authoritativeKeys(provider eventschema.Provider, weekly bool) (usedKey, resetKey string, isRemaining bool, source string) {
	switch provider {
	case eventschema.ProviderAnthropic:
		if weekly {
			return "seven_day_used_pct", "seven_day_reset_at", false, "anthropic_cookie:seven_day"
		}
		return "five_hour_used_pct", "five_hour_reset_at", false, "anthropic_cookie:five_hour"
	case eventschema.ProviderOpenAI:
		if weekly {
			return "secondary_used_pct", "secondary_resets_at", false, "codex:secondary"
		}
		return "primary_used_pct", "primary_resets_at", false, "codex:primary"
	case eventschema.ProviderGitHub:
		return "percent_remaining", "quota_reset_date", true, "copilot:quota"
	default:
		return "", "", false, ""
	}
}

// parseResetsIn turns a reset marker into a duration from now. It accepts
// unix seconds (Codex), RFC3339 (Anthropic cookie), and a plain date
// (Copilot). A missing/unparseable/past marker yields 0, so the caller
// falls back to the plan's nominal window length.
func parseResetsIn(s string, now time.Time) time.Duration {
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return positiveOrZero(time.Unix(n, 0).UTC().Sub(now))
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return positiveOrZero(t.UTC().Sub(now))
		}
	}
	return 0
}

func positiveOrZero(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return 0
}

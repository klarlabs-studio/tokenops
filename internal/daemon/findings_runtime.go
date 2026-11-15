package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// The session analysis reads a week of transcripts and takes minutes on a
// busy machine, so the glance cannot run it per answer. The daemon runs
// it a few minutes after start, once ingestion has caught up, and every
// three hours after, and the findings read the stored answer. It keeps
// what each transcript yielded between runs, so only the first run reads
// the whole week; later ones read what was written since.
const (
	sessionFindingsDelay = 5 * time.Minute
	sessionFindingsEvery = 3 * time.Hour
)

func startSessionFindingsRuntime(cache *sessions.TranscriptCache, agg *analytics.Aggregator, sup *lifecycle.Supervisor, logger *slog.Logger) {
	sup.Go("session-findings", func(ctx context.Context) error {
		runSessionFindings(ctx, findings.DefaultDir(), cache, sessionCosts(agg), sessionFindingsDelay, sessionFindingsEvery, logger)
		return nil
	})
}

// costSource reads what each session's turns cost at list prices, as of a
// run's start. A nil source leaves the effort rows' costs out.
type costSource func(ctx context.Context, now time.Time) agentdx.CostOf

// sessionCosts reads the store's turns for the analysed week. Nil without
// an aggregator.
func sessionCosts(agg *analytics.Aggregator) costSource {
	if agg == nil {
		return nil
	}
	return func(ctx context.Context, now time.Time) agentdx.CostOf {
		turns, err := agg.SessionTurns(ctx, analytics.Filter{Since: now.AddDate(0, 0, -findings.SessionDays), Until: now})
		if err != nil {
			return nil
		}
		return costOfTurns(turns)
	}
}

// costOfTurns prices a unit from the turns of its session, at list prices.
func costOfTurns(turns []analytics.SessionTurn) agentdx.CostOf {
	by := map[string][]agentdx.TurnCost{}
	for _, t := range turns {
		by[t.SessionID] = append(by[t.SessionID], agentdx.TurnCost{At: t.At, USD: t.APIEquivalentUSD})
	}
	return agentdx.UnitCosts(by)
}

func runSessionFindings(ctx context.Context, dir string, cache *sessions.TranscriptCache, costs costSource, delay, every time.Duration, logger *slog.Logger) {
	wait := time.NewTimer(delay)
	defer wait.Stop()
	select {
	case <-ctx.Done():
		return
	case <-wait.C:
	}
	if cache == nil {
		cache = sessions.NewTranscriptCache()
	}
	analyze := func() {
		start := time.Now()
		var cost agentdx.CostOf
		if costs != nil {
			cost = costs(ctx, start)
		}
		s, dropped, err := findings.AnalyzeSessions(ctx, dir, start, cache, cost)
		if len(dropped) > 0 {
			logger.Warn("session analysis figures not a number, stored as 0", "fields", dropped)
		}
		if err != nil {
			logger.Warn("session analysis not stored", "err", err)
			return
		}
		logger.Info("session analysis stored", "took", time.Since(start).Round(time.Second),
			"instructions", s.DX.Metrics.Prompts, "warnings", len(s.DX.Warnings),
			"transcripts_cached", cache.Len())
	}
	analyze()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			analyze()
		}
	}
}

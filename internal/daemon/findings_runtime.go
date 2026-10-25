package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
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

func startSessionFindingsRuntime(sup *lifecycle.Supervisor, logger *slog.Logger) {
	sup.Go("session-findings", func(ctx context.Context) error {
		runSessionFindings(ctx, findings.DefaultDir(), sessionFindingsDelay, sessionFindingsEvery, logger)
		return nil
	})
}

func runSessionFindings(ctx context.Context, dir string, delay, every time.Duration, logger *slog.Logger) {
	wait := time.NewTimer(delay)
	defer wait.Stop()
	select {
	case <-ctx.Done():
		return
	case <-wait.C:
	}
	cache := sessions.NewTranscriptCache()
	analyze := func() {
		start := time.Now()
		s, dropped, err := findings.AnalyzeSessions(ctx, dir, start, cache)
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

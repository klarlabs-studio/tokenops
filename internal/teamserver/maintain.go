package teamserver

import (
	"context"
	"time"
)

// MaintainEvery is how often the retention purge and releasing run.
const MaintainEvery = 6 * time.Hour

// Maintain enforces retention until ctx ends: figures past each
// organisation's retention, audit entries past the audit retention, and
// spent credentials are deleted at start and every interval, and settled
// weeks are released (aggregate reads also release on demand).
func (s *Server) Maintain(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = MaintainEvery
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		r, err := s.store.Purge(ctx, s.cfg.AuditRetentionDays)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Warn("retention purge failed; retrying next interval", "err", err)
		} else {
			s.log.Info("retention purge", "buckets", r.Buckets, "audit", r.Audit, "tokens", r.Tokens,
				"invites", r.Invites, "batches", r.Batches)
		}
		s.releaseAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// releaseAll releases every organisation's settled weeks.
func (s *Server) releaseAll(ctx context.Context) {
	ids, err := s.store.OrgIDs(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("listing organisations to release failed", "err", err)
		}
		return
	}
	for _, id := range ids {
		if err := s.release(ctx, id); err != nil && ctx.Err() == nil {
			s.log.Warn("release failed; retrying next interval", "org", id, "err", err)
		}
	}
}

package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"go.klarlabs.de/tokenops/internal/contexts/team"
)

// Released is one week released by EnsureReleased.
type Released struct {
	OrgID     string
	WeekStart time.Time
	Report    team.ReleaseReport
}

// EnsureReleased releases every week of an organisation that has settled
// (team.ReleasableAt), lies wholly inside its retention, and is not yet
// released. A released week is never recomputed.
//
// Weeks before the organisation's first figures are not released, so a
// member who joins and uploads their last fourteen days still adds to
// weeks nobody has seen yet.
func (s *Store) EnsureReleased(ctx context.Context, orgID string) ([]Released, error) {
	org, err := s.OrgByID(ctx, orgID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	latest := team.LatestReleasable(now)
	first := team.Week.Start(team.RetentionCutoff(now, org.RetentionDays))
	if first.Before(team.RetentionCutoff(now, org.RetentionDays)) {
		first = first.AddDate(0, 0, 7)
	}
	var earliest *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT min(day) FROM metric_buckets WHERE org_id = $1`, orgID).Scan(&earliest); err != nil {
		return nil, err
	}
	if earliest == nil {
		return nil, nil
	}
	if w := team.Week.Start(*earliest); w.After(first) {
		first = w
	}
	if first.After(latest) {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT week_start FROM released_weeks WHERE org_id = $1 AND week_start >= $2`, orgID, first)
	if err != nil {
		return nil, err
	}
	done := map[time.Time]bool{}
	weeks, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		return nil, err
	}
	for _, w := range weeks {
		done[w.UTC()] = true
	}
	var out []Released
	for w := first; !w.After(latest); w = w.AddDate(0, 0, 7) {
		if done[w] {
			continue
		}
		rep, released, err := s.releaseWeek(ctx, org, w)
		if err != nil {
			return out, err
		}
		if released {
			out = append(out, Released{OrgID: orgID, WeekStart: w, Report: rep})
		}
	}
	return out, nil
}

// releaseWeek computes and stores one week in one transaction. It reports
// false when another process released it first.
func (s *Store) releaseWeek(ctx context.Context, org Org, week time.Time) (team.ReleaseReport, bool, error) {
	var rep team.ReleaseReport
	released := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		// One release of an organisation's week at a time, across server
		// processes.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('tokenops-release:' || $1::text, 0))`, org.ID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM released_weeks WHERE org_id = $1 AND week_start = $2)`,
			org.ID, week).Scan(&exists); err != nil || exists {
			return err
		}
		in, err := weekInput(ctx, tx, org, week)
		if err != nil {
			return err
		}
		var cells []team.Cell
		cells, rep = team.Release(in)
		withheld := 0
		for _, c := range cells {
			if c.Suppressed {
				withheld++
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO released_weeks (org_id, week_start, min_group_size, released_at, withheld, fallback)
			VALUES ($1, $2, $3, $4, $5, $6)`, org.ID, week, org.MinGroupSize, s.now(), withheld, rep.Fallback); err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for _, c := range cells {
			t := c.Totals
			batch.Queue(`INSERT INTO released_cells (org_id, week_start, dim, period, period_start, grp, people, suppressed,
				sessions, instructions, turns, tool_calls, active_seconds, first_try, reworked, interrupted, escalated, rejected,
				tokens, cost_usd, api_equivalent_usd, unpriced_turns)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)`,
				org.ID, week, string(c.Dim), string(c.Period), c.PeriodStart, c.Group, c.People, c.Suppressed,
				t.Sessions, t.Instructions, t.Turns, t.ToolCalls, t.ActiveSeconds, t.FirstTry, t.Reworked, t.Interrupted,
				t.Escalated, t.Rejected, t.Tokens, t.CostUSD, t.APIEquivalentUSD, t.UnpricedTurns)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return err
		}
		released = true
		return nil
	})
	return rep, released, err
}

// weekInput reads one week's figures, summed over each member's devices,
// and the teams of every current member who had a machine enrolled.
func weekInput(ctx context.Context, tx pgx.Tx, org Org, week time.Time) (team.ReleaseInput, error) {
	in := team.ReleaseInput{WeekStart: week, MinGroup: org.MinGroupSize, Teams: map[string][]string{}}
	rows, err := tx.Query(ctx, `SELECT b.member_id, b.day, b.repo, b.kind, sum(b.sessions), sum(b.instructions), sum(b.turns),
		sum(b.tool_calls), sum(b.active_seconds), sum(b.first_try), sum(b.reworked), sum(b.interrupted), sum(b.escalated),
		sum(b.rejected), sum(b.tokens), sum(b.cost_usd), sum(b.api_equivalent_usd), sum(b.unpriced_turns)
		FROM metric_buckets b JOIN members m ON m.id = b.member_id
		WHERE b.org_id = $1 AND b.day >= $2 AND b.day < $3 AND m.removed_at IS NULL
		GROUP BY 1, 2, 3, 4`, org.ID, week, week.AddDate(0, 0, 7))
	if err != nil {
		return in, err
	}
	in.Contributions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (team.Contribution, error) {
		var c team.Contribution
		t := &c.Totals
		err := r.Scan(&c.Member, &c.Day, &c.Repo, &c.Kind, &t.Sessions, &t.Instructions, &t.Turns, &t.ToolCalls,
			&t.ActiveSeconds, &t.FirstTry, &t.Reworked, &t.Interrupted, &t.Escalated, &t.Rejected, &t.Tokens,
			&t.CostUSD, &t.APIEquivalentUSD, &t.UnpricedTurns)
		c.Day = c.Day.UTC()
		return c, err
	})
	if err != nil {
		return in, err
	}
	// Members who could have contributed: those with a machine enrolled
	// before the week ended, or with figures in it (uploaded later). That someone without one has no figures is
	// not about their work, and protecting it would withhold most cells of
	// an organisation whose owner never joined a machine.
	rows, err = tx.Query(ctx, `SELECT m.id, t.name FROM members m
		LEFT JOIN team_members tm ON tm.member_id = m.id LEFT JOIN teams t ON t.id = tm.team_id
		WHERE m.org_id = $1 AND m.removed_at IS NULL
		AND (EXISTS (SELECT 1 FROM devices d WHERE d.member_id = m.id AND d.created_at < $3)
			OR EXISTS (SELECT 1 FROM metric_buckets b WHERE b.member_id = m.id AND b.day >= $2 AND b.day < $3))`,
		org.ID, week, week.AddDate(0, 0, 7))
	if err != nil {
		return in, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var name *string
		if err := rows.Scan(&id, &name); err != nil {
			return in, err
		}
		if name == nil {
			if _, ok := in.Teams[id]; !ok {
				in.Teams[id] = nil
			}
			continue
		}
		in.Teams[id] = append(in.Teams[id], *name)
	}
	return in, rows.Err()
}

// ReleasedQuery selects released cells.
type ReleasedQuery struct {
	OrgID  string
	By     team.Dimension
	Period team.Period
	// Since and Until bound the periods' starts, Until exclusive.
	Since, Until time.Time
}

// ReleasedCells reads released cells, oldest period first, then by group.
func (s *Store) ReleasedCells(ctx context.Context, q ReleasedQuery) ([]team.Cell, error) {
	rows, err := s.pool.Query(ctx, `SELECT period_start, grp, people, suppressed, sessions, instructions, turns, tool_calls,
		active_seconds, first_try, reworked, interrupted, escalated, rejected, tokens, cost_usd, api_equivalent_usd, unpriced_turns
		FROM released_cells WHERE org_id = $1 AND dim = $2 AND period = $3 AND period_start >= $4 AND period_start < $5
		ORDER BY period_start, grp`, q.OrgID, string(q.By), string(q.Period), q.Since, q.Until)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (team.Cell, error) {
		c := team.Cell{Dim: q.By, Period: q.Period}
		t := &c.Totals
		err := r.Scan(&c.PeriodStart, &c.Group, &c.People, &c.Suppressed, &t.Sessions, &t.Instructions, &t.Turns, &t.ToolCalls,
			&t.ActiveSeconds, &t.FirstTry, &t.Reworked, &t.Interrupted, &t.Escalated, &t.Rejected, &t.Tokens,
			&t.CostUSD, &t.APIEquivalentUSD, &t.UnpricedTurns)
		c.PeriodStart = c.PeriodStart.UTC()
		return c, err
	})
}

// ReleasedThrough is the end (exclusive) of an organisation's newest
// released week, or the zero time.
func (s *Store) ReleasedThrough(ctx context.Context, orgID string) (time.Time, error) {
	var w *time.Time
	err := s.pool.QueryRow(ctx, `SELECT max(week_start) FROM released_weeks WHERE org_id = $1`, orgID).Scan(&w)
	if err != nil || w == nil {
		return time.Time{}, err
	}
	return w.UTC().AddDate(0, 0, 7), nil
}

// OrgIDs lists every organisation, for the maintenance loop.
func (s *Store) OrgIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM orgs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

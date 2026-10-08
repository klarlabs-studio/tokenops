package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go.klarlabs.de/tokenops/internal/contexts/team"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Errors the HTTP layer maps to answers.
var (
	ErrNotFound      = errors.New("not found")
	ErrInviteInvalid = errors.New("invite is unknown, used or expired")
	ErrUnauthorized  = errors.New("credential is unknown, expired or revoked")
	ErrConflict      = errors.New("already exists")
)

// Store is the team plane's Postgres store.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// Open connects to dsn and checks the connection.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("database url: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database unreachable: %w", err)
	}
	return &Store{pool: pool, now: time.Now}, nil
}

// Close releases the connections.
func (s *Store) Close() { s.pool.Close() }

// Migrate brings the schema up to date.
func (s *Store) Migrate(ctx context.Context) ([]int, error) { return Migrate(ctx, s.pool) }

// Ping reports whether the database answers, for the readiness probe.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// SetClock replaces the clock, for tests.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

func newID() string { return uuid.NewString() }

// Org is an organisation and its privacy settings.
type Org struct {
	ID            string
	Name          string
	MinGroupSize  int
	RetentionDays int
}

// Principal is a signed-in member.
type Principal struct {
	MemberID    string
	OrgID       string
	DisplayName string
	Role        team.Role
}

// DeviceAuth is an enrolled machine.
type DeviceAuth struct {
	DeviceID string
	MemberID string
	OrgID    string
}

// audit appends one entry inside tx.
func audit(ctx context.Context, tx pgx.Tx, orgID string, actor *Principal, action, subjectID, detail string) error {
	var actorID *string
	actorName := ""
	if actor != nil {
		actorID, actorName = &actor.MemberID, actor.DisplayName
	}
	var subject *string
	if subjectID != "" {
		subject = &subjectID
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_log (org_id, actor_id, actor_name, action, subject_id, subject_name, detail)
		VALUES ($1, $2, $3, $4, $5, COALESCE((SELECT display_name FROM members WHERE id = $5), ''), $6)`,
		orgID, actorID, actorName, action, subject, detail)
	return err
}

func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// CreateOrg creates an organisation with its owner and returns the owner,
// an admin API token and a login link token, each shown once.
func (s *Store) CreateOrg(ctx context.Context, name, ownerName string) (org Org, owner Principal, adminToken, loginToken string, err error) {
	org = Org{ID: newID(), Name: name, MinGroupSize: team.DefaultMinGroupSize, RetentionDays: team.DefaultRetentionDays}
	owner = Principal{MemberID: newID(), OrgID: org.ID, DisplayName: ownerName, Role: team.RoleOwner}
	adminToken, adminHash, err := team.NewToken(team.TokenAdmin)
	if err != nil {
		return
	}
	loginToken, loginHash, err := team.NewToken(team.TokenLogin)
	if err != nil {
		return
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO orgs (id, name, min_group_size, retention_days) VALUES ($1, $2, $3, $4)`,
			org.ID, name, org.MinGroupSize, org.RetentionDays); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO members (id, org_id, display_name, role) VALUES ($1, $2, $3, 'owner')`,
			owner.MemberID, org.ID, ownerName); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tokens (token_hash, member_id, kind) VALUES ($1, $2, 'admin')`, adminHash, owner.MemberID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tokens (token_hash, member_id, kind, expires_at) VALUES ($1, $2, 'login', $3)`,
			loginHash, owner.MemberID, s.now().Add(team.LoginTTL)); err != nil {
			return err
		}
		return audit(ctx, tx, org.ID, &owner, "org.created", "", name)
	})
	return
}

// OrgByName finds an organisation.
func (s *Store) OrgByName(ctx context.Context, name string) (Org, error) {
	var o Org
	err := s.pool.QueryRow(ctx, `SELECT id, name, min_group_size, retention_days FROM orgs WHERE name = $1`, name).
		Scan(&o.ID, &o.Name, &o.MinGroupSize, &o.RetentionDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// OrgByID reads an organisation.
func (s *Store) OrgByID(ctx context.Context, id string) (Org, error) {
	var o Org
	err := s.pool.QueryRow(ctx, `SELECT id, name, min_group_size, retention_days FROM orgs WHERE id = $1`, id).
		Scan(&o.ID, &o.Name, &o.MinGroupSize, &o.RetentionDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// SetPrivacy changes an organisation's minimum group size and retention.
func (s *Store) SetPrivacy(ctx context.Context, actor Principal, minGroup, retentionDays int) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE orgs SET min_group_size = $2, retention_days = $3 WHERE id = $1`,
			actor.OrgID, minGroup, retentionDays); err != nil {
			return err
		}
		return audit(ctx, tx, actor.OrgID, &actor, "settings.changed", "",
			fmt.Sprintf("min_group_size=%d retention_days=%d", minGroup, retentionDays))
	})
}

// Team is a team and how many members it has.
type Team struct {
	ID      string
	Name    string
	Members int
}

// CreateTeam adds a team to the actor's organisation.
func (s *Store) CreateTeam(ctx context.Context, actor Principal, name string) (Team, error) {
	t := Team{ID: newID(), Name: name}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO teams (id, org_id, name) VALUES ($1, $2, $3) ON CONFLICT (org_id, name) DO NOTHING`,
			t.ID, actor.OrgID, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrConflict
		}
		return audit(ctx, tx, actor.OrgID, &actor, "team.created", "", name)
	})
	return t, err
}

// Teams lists an organisation's teams.
func (s *Store) Teams(ctx context.Context, orgID string) ([]Team, error) {
	rows, err := s.pool.Query(ctx, `SELECT t.id, t.name, count(m.id)
		FROM teams t LEFT JOIN team_members tm ON tm.team_id = t.id
		LEFT JOIN members m ON m.id = tm.member_id AND m.removed_at IS NULL
		WHERE t.org_id = $1 GROUP BY t.id, t.name ORDER BY t.name`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Team, error) {
		var t Team
		err := r.Scan(&t.ID, &t.Name, &t.Members)
		return t, err
	})
}

// teamInOrg resolves a team by ID or name within an organisation.
func (s *Store) teamInOrg(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, orgID, ref string) (Team, error) {
	var t Team
	err := q.QueryRow(ctx, `SELECT id, name FROM teams WHERE org_id = $1 AND (name = $2 OR id::text = $2)`, orgID, ref).Scan(&t.ID, &t.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// TeamByRef resolves a team by ID or name within an organisation.
func (s *Store) TeamByRef(ctx context.Context, orgID, ref string) (Team, error) {
	return s.teamInOrg(ctx, s.pool, orgID, ref)
}

// CreateInvite mints an invite to teamRef with role, valid for ttl.
func (s *Store) CreateInvite(ctx context.Context, actor Principal, teamRef string, role team.Role, ttl time.Duration) (string, time.Time, error) {
	plain, hash, err := team.NewToken(team.TokenInvite)
	if err != nil {
		return "", time.Time{}, err
	}
	if ttl <= 0 || ttl > 30*24*time.Hour {
		ttl = team.InviteTTL
	}
	expires := s.now().Add(ttl)
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		t, err := s.teamInOrg(ctx, tx, actor.OrgID, teamRef)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO invites (id, org_id, team_id, role, token_hash, created_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, newID(), actor.OrgID, t.ID, string(role), hash, actor.MemberID, expires); err != nil {
			return err
		}
		return audit(ctx, tx, actor.OrgID, &actor, "invite.created", "", fmt.Sprintf("team=%s role=%s", t.Name, role))
	})
	return plain, expires, err
}

// Enroll redeems an invite: it creates the member, adds them to the
// invite's team, and enrols the device. The invite cannot be used again.
func (s *Store) Enroll(ctx context.Context, invite, displayName, deviceName string) (teamwire.EnrollResponse, error) {
	var out teamwire.EnrollResponse
	devToken, devHash, err := team.NewToken(team.TokenDevice)
	if err != nil {
		return out, err
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var inviteID, role string
		err := tx.QueryRow(ctx, `SELECT i.id, i.org_id, o.name, i.team_id, t.name, i.role
			FROM invites i JOIN orgs o ON o.id = i.org_id JOIN teams t ON t.id = i.team_id
			WHERE i.token_hash = $1 AND i.used_at IS NULL AND i.expires_at > $2 FOR UPDATE OF i`,
			team.HashToken(invite), s.now()).Scan(&inviteID, &out.OrgID, &out.OrgName, &out.TeamID, &out.TeamName, &role)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInviteInvalid
		}
		if err != nil {
			return err
		}
		out.MemberID, out.DeviceID = newID(), newID()
		if _, err := tx.Exec(ctx, `INSERT INTO members (id, org_id, display_name, role) VALUES ($1, $2, $3, $4)`,
			out.MemberID, out.OrgID, displayName, role); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO team_members (team_id, member_id) VALUES ($1, $2)`, out.TeamID, out.MemberID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO devices (id, org_id, member_id, name, token_hash) VALUES ($1, $2, $3, $4, $5)`,
			out.DeviceID, out.OrgID, out.MemberID, deviceName, devHash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invites SET used_at = $2, used_by = $3 WHERE id = $1`, inviteID, s.now(), out.MemberID); err != nil {
			return err
		}
		self := Principal{MemberID: out.MemberID, OrgID: out.OrgID, DisplayName: displayName}
		return audit(ctx, tx, out.OrgID, &self, "device.enrolled", out.MemberID, "team="+out.TeamName+" device="+deviceName)
	})
	if err != nil {
		return teamwire.EnrollResponse{}, err
	}
	out.DeviceToken = devToken
	return out, nil
}

// AuthDevice resolves a device token.
func (s *Store) AuthDevice(ctx context.Context, plain string) (DeviceAuth, error) {
	var d DeviceAuth
	err := s.pool.QueryRow(ctx, `SELECT d.id, d.member_id, d.org_id FROM devices d JOIN members m ON m.id = d.member_id
		WHERE d.token_hash = $1 AND d.revoked_at IS NULL AND m.removed_at IS NULL`, team.HashToken(plain)).
		Scan(&d.DeviceID, &d.MemberID, &d.OrgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrUnauthorized
	}
	return d, err
}

// Member reads a member as a principal.
func (s *Store) Member(ctx context.Context, memberID string) (Principal, error) {
	var p Principal
	var role string
	err := s.pool.QueryRow(ctx, `SELECT id, org_id, display_name, role FROM members WHERE id = $1 AND removed_at IS NULL`, memberID).
		Scan(&p.MemberID, &p.OrgID, &p.DisplayName, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	p.Role = team.Role(role)
	return p, err
}

// AuthToken resolves a session or admin token.
func (s *Store) AuthToken(ctx context.Context, plain string, kind team.TokenKind) (Principal, error) {
	dbKind := map[team.TokenKind]string{team.TokenSession: "session", team.TokenAdmin: "admin"}[kind]
	if dbKind == "" {
		return Principal{}, ErrUnauthorized
	}
	var p Principal
	var role string
	err := s.pool.QueryRow(ctx, `SELECT m.id, m.org_id, m.display_name, m.role FROM tokens t JOIN members m ON m.id = t.member_id
		WHERE t.token_hash = $1 AND t.kind = $2 AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at > $3)
		AND m.removed_at IS NULL`, team.HashToken(plain), dbKind, s.now()).Scan(&p.MemberID, &p.OrgID, &p.DisplayName, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrUnauthorized
	}
	p.Role = team.Role(role)
	return p, err
}

// MintLogin issues a single-use web login token for a member.
func (s *Store) MintLogin(ctx context.Context, memberID string) (string, time.Time, error) {
	plain, hash, err := team.NewToken(team.TokenLogin)
	if err != nil {
		return "", time.Time{}, err
	}
	expires := s.now().Add(team.LoginTTL)
	_, err = s.pool.Exec(ctx, `INSERT INTO tokens (token_hash, member_id, kind, expires_at) VALUES ($1, $2, 'login', $3)`, hash, memberID, expires)
	return plain, expires, err
}

// MintAdminToken issues an API token for an owner or admin.
func (s *Store) MintAdminToken(ctx context.Context, actor Principal) (string, error) {
	if !actor.Role.CanAdminister() {
		return "", errors.New("only an owner or admin holds an API token")
	}
	plain, hash, err := team.NewToken(team.TokenAdmin)
	if err != nil {
		return "", err
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tokens (token_hash, member_id, kind) VALUES ($1, $2, 'admin')`, hash, actor.MemberID); err != nil {
			return err
		}
		return audit(ctx, tx, actor.OrgID, &actor, "admin_token.created", actor.MemberID, "")
	})
	return plain, err
}

// RedeemLogin exchanges a login token, once, for a browser session token.
func (s *Store) RedeemLogin(ctx context.Context, plain string) (string, Principal, error) {
	session, hash, err := team.NewToken(team.TokenSession)
	if err != nil {
		return "", Principal{}, err
	}
	var p Principal
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var memberID string
		err := tx.QueryRow(ctx, `UPDATE tokens SET used_at = $2 WHERE token_hash = $1 AND kind = 'login'
			AND used_at IS NULL AND revoked_at IS NULL AND expires_at > $2 RETURNING member_id`, team.HashToken(plain), s.now()).Scan(&memberID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnauthorized
		}
		if err != nil {
			return err
		}
		var role string
		if err := tx.QueryRow(ctx, `SELECT id, org_id, display_name, role FROM members WHERE id = $1 AND removed_at IS NULL`, memberID).
			Scan(&p.MemberID, &p.OrgID, &p.DisplayName, &role); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrUnauthorized
			}
			return err
		}
		p.Role = team.Role(role)
		_, err = tx.Exec(ctx, `INSERT INTO tokens (token_hash, member_id, kind, expires_at) VALUES ($1, $2, 'session', $3)`,
			hash, memberID, s.now().Add(team.SessionTTL))
		return err
	})
	if err != nil {
		return "", Principal{}, err
	}
	return session, p, nil
}

// RevokeToken ends a session or admin token.
func (s *Store) RevokeToken(ctx context.Context, plain string) error {
	_, err := s.pool.Exec(ctx, `UPDATE tokens SET revoked_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`, team.HashToken(plain), s.now())
	return err
}

// Ingest applies an upload from a device. A replayed batch is acknowledged
// and not applied; a day for which a newer computation is held is left as
// it is; every other day the upload covers is replaced whole.
func (s *Store) Ingest(ctx context.Context, dev DeviceAuth, u teamwire.Upload) (teamwire.IngestResponse, error) {
	var out teamwire.IngestResponse
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO ingest_batches (device_id, batch_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, dev.DeviceID, u.BatchID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			out.Duplicate = true
			return nil
		}
		apply := map[string]bool{}
		for _, day := range u.Days {
			var held time.Time
			err := tx.QueryRow(ctx, `SELECT computed_at FROM device_days WHERE device_id = $1 AND day = $2 FOR UPDATE`, dev.DeviceID, day).Scan(&held)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return err
			case held.After(u.ComputedAt):
				out.Stale = true
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO device_days (device_id, day, computed_at) VALUES ($1, $2, $3)
				ON CONFLICT (device_id, day) DO UPDATE SET computed_at = EXCLUDED.computed_at`, dev.DeviceID, day, u.ComputedAt); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM metric_buckets WHERE device_id = $1 AND day = $2`, dev.DeviceID, day); err != nil {
				return err
			}
			apply[day] = true
		}
		var rows [][]any
		for _, b := range u.Buckets {
			if !apply[b.Day] {
				continue
			}
			day, _ := time.Parse(teamwire.DayLayout, b.Day)
			rows = append(rows, []any{dev.DeviceID, dev.MemberID, dev.OrgID, day, b.Repo, b.Kind,
				b.Sessions, b.Instructions, b.Turns, b.ToolCalls, b.ActiveSeconds, b.FirstTry, b.Reworked,
				b.Interrupted, b.Escalated, b.Rejected, b.Tokens, b.CostUSD, b.APIEquivalentUSD, b.UnpricedTurns})
		}
		if len(rows) > 0 {
			n, err := tx.CopyFrom(ctx, pgx.Identifier{"metric_buckets"}, []string{"device_id", "member_id", "org_id", "day", "repo", "kind",
				"sessions", "instructions", "turns", "tool_calls", "active_seconds", "first_try", "reworked",
				"interrupted", "escalated", "rejected", "tokens", "cost_usd", "api_equivalent_usd", "unpriced_turns"}, pgx.CopyFromRows(rows))
			if err != nil {
				return err
			}
			out.Accepted = int(n)
		}
		_, err = tx.Exec(ctx, `UPDATE devices SET last_seen_at = $2 WHERE id = $1`, dev.DeviceID, s.now())
		return err
	})
	return out, err
}

// RevokeDevice stops a device's uploads. With erase, its figures are
// deleted too.
func (s *Store) RevokeDevice(ctx context.Context, actor Principal, deviceID string, erase bool) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var owner string
		err := tx.QueryRow(ctx, `UPDATE devices SET revoked_at = COALESCE(revoked_at, $3) WHERE id = $1 AND org_id = $2 RETURNING member_id`,
			deviceID, actor.OrgID, s.now()).Scan(&owner)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		detail := "kept its figures"
		if erase {
			if _, err := tx.Exec(ctx, `DELETE FROM metric_buckets WHERE device_id = $1`, deviceID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM device_days WHERE device_id = $1`, deviceID); err != nil {
				return err
			}
			detail = "erased its figures"
		}
		return audit(ctx, tx, actor.OrgID, &actor, "device.revoked", owner, detail)
	})
}

// RemoveMember removes a member from the organisation: their devices,
// figures, credentials and grants are deleted. The audit log keeps their
// name against what they did and what was done about them.
func (s *Store) RemoveMember(ctx context.Context, actor Principal, memberID string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var role string
		err := tx.QueryRow(ctx, `SELECT role FROM members WHERE id = $1 AND org_id = $2 AND removed_at IS NULL FOR UPDATE`, memberID, actor.OrgID).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if role == string(team.RoleOwner) {
			var owners int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM members WHERE org_id = $1 AND role = 'owner' AND removed_at IS NULL`, actor.OrgID).Scan(&owners); err != nil {
				return err
			}
			if owners <= 1 {
				return errors.New("the last owner cannot be removed")
			}
		}
		if err := audit(ctx, tx, actor.OrgID, &actor, "member.removed", memberID, "devices, figures, credentials and grants deleted"); err != nil {
			return err
		}
		for _, q := range []string{
			`DELETE FROM metric_buckets WHERE member_id = $1`,
			`DELETE FROM devices WHERE member_id = $1`,
			`DELETE FROM tokens WHERE member_id = $1`,
			`DELETE FROM grants WHERE grantee_id = $1`,
			`DELETE FROM team_members WHERE member_id = $1`,
			`UPDATE members SET removed_at = now() WHERE id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, memberID); err != nil {
				return err
			}
		}
		return nil
	})
}

// sums is the SELECT list every aggregate shares, in Totals' order.
const sums = `count(DISTINCT b.member_id), sum(b.sessions), sum(b.instructions), sum(b.turns), sum(b.tool_calls),
	sum(b.active_seconds), sum(b.first_try), sum(b.reworked), sum(b.interrupted), sum(b.escalated), sum(b.rejected),
	sum(b.tokens), sum(b.cost_usd), sum(b.api_equivalent_usd), sum(b.unpriced_turns)`

func scanRow(r pgx.CollectableRow) (team.Row, error) {
	var row team.Row
	t := &row.Totals
	err := r.Scan(&row.PeriodStart, &row.Group, &row.People, &t.Sessions, &t.Instructions, &t.Turns, &t.ToolCalls,
		&t.ActiveSeconds, &t.FirstTry, &t.Reworked, &t.Interrupted, &t.Escalated, &t.Rejected,
		&t.Tokens, &t.CostUSD, &t.APIEquivalentUSD, &t.UnpricedTurns)
	return row, err
}

// Query selects figures to aggregate.
type Query struct {
	OrgID  string
	By     team.Dimension
	Period team.Period
	// Since and Until bound the days, Until exclusive.
	Since, Until time.Time
	// TeamID narrows a repo or kind breakdown to one team's members.
	TeamID string
}

// Aggregate sums the figures by period and dimension, with how many
// people contributed to each row. It does not suppress: the caller applies
// team.Suppress with the organisation's minimum group size.
func (s *Store) Aggregate(ctx context.Context, q Query) ([]team.Row, error) {
	var teamFilter *string
	if q.TeamID != "" {
		teamFilter = &q.TeamID
	}
	var sql string
	switch q.By {
	case team.ByTeam:
		sql = `SELECT date_trunc($4::text, b.day::timestamp)::date, t.name, ` + sums + `
			FROM metric_buckets b JOIN team_members tm ON tm.member_id = b.member_id JOIN teams t ON t.id = tm.team_id
			WHERE b.org_id = $1 AND b.day >= $2 AND b.day < $3 AND ($5::uuid IS NULL OR t.id = $5)
			GROUP BY 1, 2 ORDER BY 1, 2`
	case team.ByRepo, team.ByKind:
		col := map[team.Dimension]string{team.ByRepo: "b.repo", team.ByKind: "b.kind"}[q.By]
		sql = `SELECT date_trunc($4::text, b.day::timestamp)::date, ` + col + `, ` + sums + `
			FROM metric_buckets b
			WHERE b.org_id = $1 AND b.day >= $2 AND b.day < $3
			AND ($5::uuid IS NULL OR b.member_id IN (SELECT member_id FROM team_members WHERE team_id = $5))
			GROUP BY 1, 2 ORDER BY 1, 2`
	default:
		return nil, fmt.Errorf("dimension %q", q.By)
	}
	rows, err := s.pool.Query(ctx, sql, q.OrgID, q.Since, q.Until, string(q.Period), teamFilter)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanRow)
}

// MemberSeries sums one member's figures by period and kind of work.
func (s *Store) MemberSeries(ctx context.Context, memberID string, period team.Period, since, until time.Time) ([]team.Row, error) {
	rows, err := s.pool.Query(ctx, `SELECT date_trunc($4::text, b.day::timestamp)::date, b.kind, `+sums+`
		FROM metric_buckets b WHERE b.member_id = $1 AND b.day >= $2 AND b.day < $3
		GROUP BY 1, 2 ORDER BY 1, 2`, memberID, since, until, string(period))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanRow)
}

// MemberInfo is a member as an administrator or grantee lists them.
type MemberInfo struct {
	ID          string
	DisplayName string
	Role        team.Role
	TeamIDs     []string
	TeamNames   []string
}

// Members lists an organisation's current members with their teams.
func (s *Store) Members(ctx context.Context, orgID string) ([]MemberInfo, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.id, m.display_name, m.role,
		COALESCE(array_agg(t.id::text ORDER BY t.name) FILTER (WHERE t.id IS NOT NULL), '{}'),
		COALESCE(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.id IS NOT NULL), '{}')
		FROM members m LEFT JOIN team_members tm ON tm.member_id = m.id LEFT JOIN teams t ON t.id = tm.team_id
		WHERE m.org_id = $1 AND m.removed_at IS NULL GROUP BY m.id ORDER BY m.display_name`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (MemberInfo, error) {
		var m MemberInfo
		var role string
		err := r.Scan(&m.ID, &m.DisplayName, &role, &m.TeamIDs, &m.TeamNames)
		m.Role = team.Role(role)
		return m, err
	})
}

// MemberInOrg reads one member of an organisation with their teams.
func (s *Store) MemberInOrg(ctx context.Context, orgID, memberID string) (MemberInfo, error) {
	all, err := s.Members(ctx, orgID)
	if err != nil {
		return MemberInfo{}, err
	}
	for _, m := range all {
		if m.ID == memberID {
			return m, nil
		}
	}
	return MemberInfo{}, ErrNotFound
}

// AddToTeam puts a member in another team.
func (s *Store) AddToTeam(ctx context.Context, actor Principal, memberID, teamRef string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		t, err := s.teamInOrg(ctx, tx, actor.OrgID, teamRef)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO team_members (team_id, member_id)
			SELECT $1, id FROM members WHERE id = $2 AND org_id = $3 AND removed_at IS NULL ON CONFLICT DO NOTHING`, t.ID, memberID, actor.OrgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return audit(ctx, tx, actor.OrgID, &actor, "member.added_to_team", memberID, t.Name)
	})
}

// SetRole changes a member's role.
func (s *Store) SetRole(ctx context.Context, actor Principal, memberID string, role team.Role) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE members SET role = $3 WHERE id = $1 AND org_id = $2 AND removed_at IS NULL`, memberID, actor.OrgID, string(role))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit(ctx, tx, actor.OrgID, &actor, "member.role_changed", memberID, string(role))
	})
}

// GrantInfo is a grant with names for display.
type GrantInfo struct {
	team.Grant
	GranteeName string
	GranteeRole team.Role
	TeamName    string
	Reason      string
	GrantedBy   string
	CreatedAt   time.Time
}

// Grants lists an organisation's grants, active ones first.
func (s *Store) Grants(ctx context.Context, orgID string) ([]GrantInfo, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.id, g.grantee_id, COALESCE(g.team_id::text, ''), g.revoked_at,
		m.display_name, m.role, COALESCE(t.name, ''), g.reason, COALESCE(gb.display_name, ''), g.created_at
		FROM grants g JOIN members m ON m.id = g.grantee_id LEFT JOIN teams t ON t.id = g.team_id
		LEFT JOIN members gb ON gb.id = g.granted_by
		WHERE g.org_id = $1 ORDER BY g.revoked_at IS NOT NULL, g.created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (GrantInfo, error) {
		var g GrantInfo
		var role string
		err := r.Scan(&g.ID, &g.GranteeID, &g.TeamID, &g.RevokedAt, &g.GranteeName, &role, &g.TeamName, &g.Reason, &g.GrantedBy, &g.CreatedAt)
		g.GranteeRole = team.Role(role)
		return g, err
	})
}

// ActiveGrants are the grants in force, as the access rule takes them.
func (s *Store) ActiveGrants(ctx context.Context, orgID string) ([]team.Grant, error) {
	all, err := s.Grants(ctx, orgID)
	if err != nil {
		return nil, err
	}
	var out []team.Grant
	for _, g := range all {
		if g.Active() {
			out = append(out, g.Grant)
		}
	}
	return out, nil
}

// CreateGrant lets grantee see the individual figures of teamRef's
// members, or everyone's when teamRef is empty.
func (s *Store) CreateGrant(ctx context.Context, actor Principal, granteeID, teamRef, reason string) (string, error) {
	id := newID()
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var role string
		err := tx.QueryRow(ctx, `SELECT role FROM members WHERE id = $1 AND org_id = $2 AND removed_at IS NULL`, granteeID, actor.OrgID).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !team.Role(role).CanHoldGrant() {
			return fmt.Errorf("a %s cannot be granted individual views; make them a lead first", role)
		}
		var teamID *string
		scope := "everyone"
		if teamRef != "" {
			t, err := s.teamInOrg(ctx, tx, actor.OrgID, teamRef)
			if err != nil {
				return err
			}
			teamID, scope = &t.ID, "team "+t.Name
		}
		if _, err := tx.Exec(ctx, `INSERT INTO grants (id, org_id, grantee_id, team_id, reason, granted_by) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, actor.OrgID, granteeID, teamID, reason, actor.MemberID); err != nil {
			return err
		}
		return audit(ctx, tx, actor.OrgID, &actor, "grant.created", granteeID, scope+": "+reason)
	})
	return id, err
}

// RevokeGrant ends a grant.
func (s *Store) RevokeGrant(ctx context.Context, actor Principal, grantID string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var grantee string
		err := tx.QueryRow(ctx, `UPDATE grants SET revoked_at = $3, revoked_by = $4 WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL
			RETURNING grantee_id`, grantID, actor.OrgID, s.now(), actor.MemberID).Scan(&grantee)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return audit(ctx, tx, actor.OrgID, &actor, "grant.revoked", grantee, grantID)
	})
}

// RecordView logs that viewer saw subject's individual figures, under
// which grant. The subject sees it on their page.
func (s *Store) RecordView(ctx context.Context, viewer Principal, subjectID, grantID string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		return audit(ctx, tx, viewer.OrgID, &viewer, "member.viewed", subjectID, "grant="+grantID)
	})
}

// AuditEntry is one line of the audit log.
type AuditEntry struct {
	At      time.Time
	Actor   string
	Action  string
	Subject string
	Detail  string
}

// Audit lists an organisation's newest audit entries.
func (s *Store) Audit(ctx context.Context, orgID string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT at, actor_name, action, subject_name, detail FROM audit_log
		WHERE org_id = $1 ORDER BY at DESC, id DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AuditEntry, error) {
		var e AuditEntry
		err := r.Scan(&e.At, &e.Actor, &e.Action, &e.Subject, &e.Detail)
		return e, err
	})
}

// Me is what the store holds about a member and who can see it.
func (s *Store) Me(ctx context.Context, memberID string) (teamwire.Me, error) {
	var me teamwire.Me
	var orgID string
	err := s.pool.QueryRow(ctx, `SELECT o.id, o.name, o.retention_days, o.min_group_size, m.id, m.display_name, m.role
		FROM members m JOIN orgs o ON o.id = m.org_id WHERE m.id = $1 AND m.removed_at IS NULL`, memberID).
		Scan(&orgID, &me.OrgName, &me.RetentionDays, &me.MinGroupSize, &me.MemberID, &me.DisplayName, &me.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return me, ErrNotFound
	}
	if err != nil {
		return me, err
	}
	info, err := s.MemberInOrg(ctx, orgID, memberID)
	if err != nil {
		return me, err
	}
	me.Teams = append([]string{}, info.TeamNames...)
	rows, err := s.pool.Query(ctx, `SELECT id, name, created_at, last_seen_at, revoked_at FROM devices WHERE member_id = $1 ORDER BY created_at`, memberID)
	if err != nil {
		return me, err
	}
	me.Devices, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (teamwire.Device, error) {
		var d teamwire.Device
		err := r.Scan(&d.ID, &d.Name, &d.EnrolledAt, &d.LastSeenAt, &d.RevokedAt)
		return d, err
	})
	if err != nil {
		return me, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT day) FROM metric_buckets WHERE member_id = $1`, memberID).
		Scan(&me.Buckets, &me.Days); err != nil {
		return me, err
	}
	grants, err := s.Grants(ctx, orgID)
	if err != nil {
		return me, err
	}
	me.Viewers = []teamwire.Viewer{}
	for _, g := range grants {
		if !g.Covers(info.TeamIDs) || g.GranteeID == memberID || !g.GranteeRole.CanHoldGrant() {
			continue
		}
		scope := "everyone"
		if g.TeamName != "" {
			scope = "team " + g.TeamName
		}
		me.Viewers = append(me.Viewers, teamwire.Viewer{Name: g.GranteeName, Role: string(g.GranteeRole), Scope: scope, Reason: g.Reason, GrantedAt: g.CreatedAt})
	}
	rows, err = s.pool.Query(ctx, `SELECT actor_name, at FROM audit_log WHERE subject_id = $1 AND action = 'member.viewed'
		AND actor_id IS DISTINCT FROM $1 ORDER BY at DESC LIMIT 100`, memberID)
	if err != nil {
		return me, err
	}
	me.Views, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (teamwire.View, error) {
		var v teamwire.View
		err := r.Scan(&v.Viewer, &v.At)
		return v, err
	})
	if me.Views == nil {
		me.Views = []teamwire.View{}
	}
	return me, err
}

// PurgeResult counts what a purge deleted.
type PurgeResult struct {
	Buckets, Audit, Tokens, Invites, Batches int64
}

// Purge deletes figures older than each organisation's retention, audit
// entries older than auditDays, and spent credentials.
func (s *Store) Purge(ctx context.Context, auditDays int) (PurgeResult, error) {
	var r PurgeResult
	now := s.now().UTC()
	if auditDays < team.MinRetentionDays {
		auditDays = team.DefaultAuditRetentionDays
	}
	orgs, err := s.pool.Query(ctx, `SELECT id, retention_days FROM orgs`)
	if err != nil {
		return r, err
	}
	type orgRet struct {
		id   string
		days int
	}
	list, err := pgx.CollectRows(orgs, func(row pgx.CollectableRow) (orgRet, error) {
		var o orgRet
		err := row.Scan(&o.id, &o.days)
		return o, err
	})
	if err != nil {
		return r, err
	}
	for _, o := range list {
		cutoff := team.RetentionCutoff(now, o.days)
		tag, err := s.pool.Exec(ctx, `DELETE FROM metric_buckets WHERE org_id = $1 AND day < $2`, o.id, cutoff)
		if err != nil {
			return r, err
		}
		r.Buckets += tag.RowsAffected()
		if _, err := s.pool.Exec(ctx, `DELETE FROM device_days dd USING devices d WHERE dd.device_id = d.id AND d.org_id = $1 AND dd.day < $2`, o.id, cutoff); err != nil {
			return r, err
		}
	}
	steps := []struct {
		n   *int64
		sql string
		arg any
	}{
		{&r.Audit, `DELETE FROM audit_log WHERE at < $1`, now.AddDate(0, 0, -auditDays)},
		{&r.Tokens, `DELETE FROM tokens WHERE (expires_at IS NOT NULL AND expires_at < $1) OR revoked_at < $1 OR (kind = 'login' AND used_at IS NOT NULL)`, now},
		{&r.Invites, `DELETE FROM invites WHERE expires_at < $1`, now.AddDate(0, 0, -30)},
		{&r.Batches, `DELETE FROM ingest_batches WHERE received_at < $1`, now.AddDate(0, 0, -30)},
	}
	for _, st := range steps {
		tag, err := s.pool.Exec(ctx, st.sql, st.arg)
		if err != nil {
			return r, err
		}
		*st.n = tag.RowsAffected()
	}
	return r, nil
}

// FindMember resolves a member of an organisation by ID or exact display
// name, for the server's command line.
func (s *Store) FindMember(ctx context.Context, orgID, ref string) (MemberInfo, error) {
	all, err := s.Members(ctx, orgID)
	if err != nil {
		return MemberInfo{}, err
	}
	var hit []MemberInfo
	for _, m := range all {
		if m.ID == ref || strings.EqualFold(m.DisplayName, ref) {
			hit = append(hit, m)
		}
	}
	switch len(hit) {
	case 0:
		return MemberInfo{}, ErrNotFound
	case 1:
		return hit[0], nil
	}
	return MemberInfo{}, fmt.Errorf("%d members are called %q; use the member ID", len(hit), ref)
}

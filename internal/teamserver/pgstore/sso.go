package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go.klarlabs.de/tokenops/internal/contexts/team"
)

// SetSSO configures, or reconfigures, an organisation's OpenID Connect
// sign-in. Only the server's console calls it: the issuer is a URL the
// server will fetch, and the secret reference names the server's own
// environment or files.
func (s *Store) SetSSO(ctx context.Context, actor Principal, c team.SSO) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO org_sso (org_id, issuer, client_id, client_secret_ref, allowed_domains, allow_unverified_email, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (org_id) DO UPDATE SET issuer = $2, client_id = $3, client_secret_ref = $4, allowed_domains = $5,
			allow_unverified_email = $6, updated_at = $7`,
			actor.OrgID, c.Issuer, c.ClientID, c.SecretRef, c.Domains, c.AllowUnverifiedEmail, s.now()); err != nil {
			return err
		}
		detail := fmt.Sprintf("issuer=%s domains=%s", c.Issuer, strings.Join(c.Domains, ","))
		if c.AllowUnverifiedEmail {
			detail += " unverified_email=allowed"
		}
		return audit(ctx, tx, actor.OrgID, &actor, "sso.configured", "", detail)
	})
}

// DisableSSO turns an organisation's single sign-on off. Sessions it
// started run out on their own (twelve hours).
func (s *Store) DisableSSO(ctx context.Context, actor Principal) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM org_sso WHERE org_id = $1`, actor.OrgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit(ctx, tx, actor.OrgID, &actor, "sso.disabled", "", "")
	})
}

// SSOConfig reads an organisation's single sign-on, or ErrNotFound.
func (s *Store) SSOConfig(ctx context.Context, orgID string) (team.SSO, error) {
	var c team.SSO
	err := s.pool.QueryRow(ctx, `SELECT issuer, client_id, client_secret_ref, allowed_domains, allow_unverified_email
		FROM org_sso WHERE org_id = $1`, orgID).Scan(&c.Issuer, &c.ClientID, &c.SecretRef, &c.Domains, &c.AllowUnverifiedEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// SSOEnabled reports whether any organisation on this server has single
// sign-on, so the sign-in page offers it.
func (s *Store) SSOEnabled(ctx context.Context) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM org_sso)`).Scan(&ok)
	return ok, err
}

// SetEmail sets the address single sign-on signs a member in by; "" clears
// it. Two current members of one organisation cannot share an address.
func (s *Store) SetEmail(ctx context.Context, actor Principal, memberID, email string) error {
	var value *string
	if email != "" {
		e, err := team.NormalizeEmail(email)
		if err != nil {
			return err
		}
		value = &e
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE members SET email = $3 WHERE id = $1 AND org_id = $2 AND removed_at IS NULL`, memberID, actor.OrgID, value)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("another member already has this e-mail address: %w", ErrConflict)
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		detail := "cleared"
		if value != nil {
			detail = *value
		}
		return audit(ctx, tx, actor.OrgID, &actor, "member.email_set", memberID, detail)
	})
}

// MemberByEmail finds the current member of an organisation an address is
// set on.
func (s *Store) MemberByEmail(ctx context.Context, orgID, email string) (Principal, error) {
	var p Principal
	var role string
	err := s.pool.QueryRow(ctx, `SELECT id, org_id, display_name, role FROM members
		WHERE org_id = $1 AND email = $2 AND removed_at IS NULL`, orgID, email).Scan(&p.MemberID, &p.OrgID, &p.DisplayName, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	p.Role = team.Role(role)
	return p, err
}

// SSOLogin is a sign-in in flight.
type SSOLogin struct {
	OrgID    string
	Nonce    string
	Verifier string
}

// BeginSSO records a sign-in leaving for the issuer, under the hash of its
// state.
func (s *Store) BeginSSO(ctx context.Context, state string, l SSOLogin) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO sso_logins (state_hash, org_id, nonce, verifier, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		team.HashToken(state), l.OrgID, l.Nonce, l.Verifier, s.now().Add(team.SSOStateTTL))
	return err
}

// ConsumeSSO takes back, once, the sign-in a state was issued for. A state
// that is unknown, used or expired is ErrUnauthorized.
func (s *Store) ConsumeSSO(ctx context.Context, state string) (SSOLogin, error) {
	var l SSOLogin
	var live bool
	// Deleted whether or not it is still live: a state is never tried twice.
	err := s.pool.QueryRow(ctx, `DELETE FROM sso_logins WHERE state_hash = $1 RETURNING org_id, nonce, verifier, expires_at > $2`,
		team.HashToken(state), s.now()).Scan(&l.OrgID, &l.Nonce, &l.Verifier, &live)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !live) {
		return SSOLogin{}, ErrUnauthorized
	}
	return l, err
}

// MintSession starts a browser session for a member who signed in some
// other way than a link (single sign-on), and records how.
func (s *Store) MintSession(ctx context.Context, p Principal, via string) (string, error) {
	plain, hash, err := team.NewToken(team.TokenSession)
	if err != nil {
		return "", err
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tokens (token_hash, member_id, kind, expires_at) VALUES ($1, $2, 'session', $3)`,
			hash, p.MemberID, s.now().Add(team.SessionTTL)); err != nil {
			return err
		}
		return audit(ctx, tx, p.OrgID, &p, "member.signed_in", p.MemberID, via)
	})
	return plain, err
}

// Package fireworks reads a Fireworks account's own figures: spend this
// month and the limit it is spent against (ADR 0009 §7). FireConnect points
// Claude Code, Codex and opencode at Fireworks, and the open models it
// routes to bill there, so Fireworks is a biller with limits of its own.
//
// Only Fireworks' documented REST API is used
// (https://docs.fireworks.ai/api-reference, /nexus/usage-limits):
//
//   - GET /verifyApiKey names the key's account (x-fireworks-account-id).
//   - GET /v1/accounts/{a}/users/{u}/usageLimits is a member's own spend
//     and effective cap, on company (Nexus) accounts.
//   - GET /v1/accounts/{a}/billing/summary and the monthly-spend-usd quota
//     are an account's month spend and limit, for an account of one's own.
//
// The package holds the reading, the poller and the mapping to envelopes;
// the HTTP client and the key discovery live in
// internal/infra/vendorusage/fireworks and reach the poller through the
// Reader port. The key is fetched for each call and never kept or logged.
package fireworks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Reader reads the best reading for this operator: their own cap on a
// company account, else the account's month. The HTTP client in
// internal/infra/vendorusage/fireworks satisfies it.
type Reader interface {
	Read(ctx context.Context, account, user string, now time.Time) (Reading, error)
}

// ErrNoKey reports that no Fireworks key is on the machine: Fireworks is
// not in use here, and the reader stays idle.
var ErrNoKey = errors.New("fireworks: no API key on this machine")

var (
	// ErrAuth reports a key Fireworks refused.
	ErrAuth = errors.New("fireworks: the API key was refused")
	// ErrNotFound reports a resource the account does not have.
	ErrNotFound = errors.New("fireworks: not found")
)

// Scope says whose figures a Reading holds.
type Scope string

const (
	// ScopeUser is a member's own spend against their cap.
	ScopeUser Scope = "user"
	// ScopeAccount is the whole account's spend against its limit.
	ScopeAccount Scope = "account"
)

// Reading is the month's spend and limit, as Fireworks reports them.
type Reading struct {
	Scope     Scope
	AccountID string
	// UsedUSD is spend this billing period.
	UsedUSD float64
	// LimitUSD is the cap it is spent against, 0 when there is none.
	LimitUSD float64
	// LimitReached is Fireworks saying requests are blocked by the cap.
	LimitReached bool
}

// Identity is the account and user FireConnect signed in as, read from
// the ids-only files it keeps: minted-key.json names the user
// ("accounts/<a>/users/<u>"), config.json the SSO account. Either may be
// empty. config.json can also hold keys, so only ssoAccountId is decoded.
func Identity(home string) (account, user string) {
	dir := filepath.Join(home, ".fireconnect")
	if b, err := os.ReadFile(filepath.Join(dir, "minted-key.json")); err == nil { //nolint:gosec // fixed FireConnect path
		var m struct {
			UserName string `json:"userName"`
		}
		if json.Unmarshal(b, &m) == nil {
			parts := strings.Split(m.UserName, "/")
			if len(parts) == 4 && parts[0] == "accounts" && parts[2] == "users" {
				account, user = parts[1], parts[3]
			}
		}
	}
	if account == "" {
		if b, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil { //nolint:gosec // fixed FireConnect path
			var c struct {
				SSOAccountID string `json:"ssoAccountId"`
			}
			if json.Unmarshal(b, &c) == nil {
				account = c.SSOAccountID
			}
		}
	}
	return account, user
}

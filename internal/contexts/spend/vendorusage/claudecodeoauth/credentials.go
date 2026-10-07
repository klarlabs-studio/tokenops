// Package claudecodeoauth reads Claude's plan windows with Claude Code's
// own sign-in: the OAuth token Claude Code keeps, sent to Anthropic's usage
// endpoint, the way CodexBar reads them (ADR 0011, source class 4).
//
// The package holds the credentials, the Store and UsageClient ports, the
// poller and its reading; the credentials file, Keychain and HTTP adapters
// live in internal/infra/vendorusage/claudecodeoauth.
//
// It is opt-in. The token belongs to another application, so it is read
// only after the operator turns this source on, held in memory only, sent
// only to api.anthropic.com, and never refreshed here: a refresh rotates
// the token and would sign Claude Code out. When it expires, Claude Code
// renews it the next time it runs.
package claudecodeoauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
)

// Credentials is the part of Claude Code's sign-in this source uses.
type Credentials struct {
	AccessToken      string
	ExpiresAt        time.Time
	Scopes           []string
	SubscriptionType string
}

// Expired reports whether the token has passed its expiry, with a minute's
// margin so a request is not sent with seconds to spare.
func (c Credentials) Expired(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && !now.Add(time.Minute).Before(c.ExpiresAt)
}

// profileScope is the scope the usage endpoint requires. A token from
// `claude setup-token` carries only user:inference and cannot read usage.
const profileScope = "user:profile"

var (
	// ErrNotSignedIn means Claude Code holds no claude.ai sign-in here.
	ErrNotSignedIn = errors.New("claude-code-oauth: Claude Code is not signed in to a Claude plan on this machine")
	// ErrNoUsageScope means the token cannot read usage.
	ErrNoUsageScope = errors.New("claude-code-oauth: Claude Code's token lacks the user:profile scope (a `claude setup-token` token cannot read usage); sign in with `claude` instead")
	// ErrKeychainDenied means macOS refused, or the operator declined,
	// the Keychain read.
	ErrKeychainDenied = errors.New("claude-code-oauth: macOS declined the Keychain read of Claude Code's sign-in")
)

// Parse reads Claude Code's credentials document: {"claudeAiOauth":
// {accessToken, expiresAt (Unix ms), scopes, subscriptionType}}. The
// refresh token is deliberately not read.
func Parse(raw []byte) (Credentials, error) {
	var doc struct {
		ClaudeAiOauth *struct {
			AccessToken      string   `json:"accessToken"`
			ExpiresAt        int64    `json:"expiresAt"`
			Scopes           []string `json:"scopes"`
			SubscriptionType string   `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Credentials{}, fmt.Errorf("claude-code-oauth: unreadable credentials: %w", err)
	}
	o := doc.ClaudeAiOauth
	if o == nil || strings.TrimSpace(o.AccessToken) == "" {
		// Claude Code 2.1 can store only MCP sign-ins under the same item.
		return Credentials{}, ErrNotSignedIn
	}
	c := Credentials{AccessToken: strings.TrimSpace(o.AccessToken), Scopes: o.Scopes, SubscriptionType: o.SubscriptionType}
	if o.ExpiresAt > 0 {
		c.ExpiresAt = time.UnixMilli(o.ExpiresAt)
	}
	if len(c.Scopes) > 0 && !slices.Contains(c.Scopes, profileScope) {
		return Credentials{}, ErrNoUsageScope
	}
	return c, nil
}

// ErrExpired means Anthropic refused the token: Claude Code renews it the
// next time it runs.
var ErrExpired = errors.New("claude-code-oauth: Claude Code's sign-in has expired; it renews it the next time it runs — open Claude Code, or run `claude`")

// RateLimitedError carries when the endpoint may be asked again.
type RateLimitedError struct{ Until time.Time }

func (e *RateLimitedError) Error() string {
	return "claude-code-oauth: Anthropic is rate-limiting usage reads until " + e.Until.Format(time.RFC3339)
}

// UsageClient reads the plan's windows with an OAuth token. The HTTP client
// in internal/infra/vendorusage/claudecodeoauth satisfies it.
type UsageClient interface {
	Usage(ctx context.Context, token string, now time.Time) (*claudeusagemeter.UsageResponse, error)
}

// Store is one place Claude Code keeps its sign-in.
type Store interface {
	Name() string
	Read(ctx context.Context) (Credentials, error)
}

// PromptingStore is a Store whose read asks the operator, as the macOS
// Keychain does. After a declined read the poller passes it over for a
// while rather than prompting again every poll.
type PromptingStore interface {
	Store
	Prompts() bool
}

// ReadFirst returns the first signed-in credentials across stores. A store
// with nothing in it is passed over; any other failure is returned when no
// store succeeds, so a denied Keychain is not reported as "not signed in".
func ReadFirst(ctx context.Context, stores []Store) (Credentials, error) {
	last := ErrNotSignedIn
	for _, s := range stores {
		c, err := s.Read(ctx)
		if err == nil {
			return c, nil
		}
		if !errors.Is(err, ErrNotSignedIn) || errors.Is(last, ErrNotSignedIn) {
			last = err
		}
	}
	return Credentials{}, last
}

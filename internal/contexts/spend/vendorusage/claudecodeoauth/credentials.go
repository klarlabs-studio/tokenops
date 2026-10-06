// Package claudecodeoauth reads Claude's plan windows with Claude Code's
// own sign-in: the OAuth token Claude Code keeps, sent to Anthropic's usage
// endpoint, the way CodexBar reads them (ADR 0011, source class 4).
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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
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

// Store is one place Claude Code keeps its sign-in.
type Store interface {
	Name() string
	Read(ctx context.Context) (Credentials, error)
}

// FileStore is ~/.claude/.credentials.json: where Claude Code keeps its
// sign-in on Linux and Windows, and on macOS when the Keychain refused the
// write.
type FileStore struct{ Path string }

// Name implements Store.
func (FileStore) Name() string { return "credentials file" }

// Read implements Store.
func (f FileStore) Read(context.Context) (Credentials, error) {
	raw, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Credentials{}, ErrNotSignedIn
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("claude-code-oauth: %w", err)
	}
	return Parse(raw)
}

// keychainService is the Keychain item Claude Code writes on macOS.
const keychainService = "Claude Code-credentials"

// KeychainStore reads the macOS Keychain item through /usr/bin/security.
// macOS asks the operator to allow it; Claude Code rewrites the item when
// it renews its token, which can ask again.
type KeychainStore struct {
	// Run executes a command and returns its stdout; nil uses exec.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Name implements Store.
func (KeychainStore) Name() string { return "macOS Keychain" }

// keychainTimeout bounds the read: a prompt nobody answers must not hold
// the poller.
const keychainTimeout = 30 * time.Second

// Read implements Store.
func (k KeychainStore) Read(ctx context.Context) (Credentials, error) {
	run := k.Run
	if run == nil {
		run = runCommand
	}
	ctx, cancel := context.WithTimeout(ctx, keychainTimeout)
	defer cancel()
	out, err := run(ctx, "/usr/bin/security", "find-generic-password", "-s", keychainService, "-w")
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.ExitCode() == 44:
		// errSecItemNotFound: Claude Code has not signed in here.
		return Credentials{}, ErrNotSignedIn
	case err != nil:
		// A denial, a cancelled prompt and a timed-out one look alike
		// from here; each means "not now", and none should be retried at
		// once.
		return Credentials{}, ErrKeychainDenied
	}
	return Parse(out)
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // fixed binary and arguments
}

// Stores are the places to look, file first: it needs no prompt. The
// Keychain is included only when the operator allowed it.
func Stores(home string, keychain bool) []Store {
	out := []Store{FileStore{Path: filepath.Join(home, ".claude", ".credentials.json")}}
	if keychain {
		out = append(out, KeychainStore{})
	}
	return out
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

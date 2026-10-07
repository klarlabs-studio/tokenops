// Package claudesignin is the use case behind connecting Claude Code's own
// sign-in as a source of Claude's plan windows (ADR 0011): read it, prove it
// reads usage, and say what Anthropic reports. The token never leaves this
// call.
package claudesignin

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodeoauth"
	claudeoauthapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/claudecodeoauth"
)

// Options say where to look and whom to ask.
type Options struct {
	Home string
	// Keychain allows the macOS Keychain read, which macOS asks about.
	Keychain bool
	// BaseURL overrides Anthropic's API in tests.
	BaseURL   string
	UserAgent string
	Now       time.Time
}

// Result is what the check found, without the token.
type Result struct {
	// Summary is Anthropic's windows in words; empty when rate-limited.
	Summary []string
	// Plan is Claude Code's subscription, as it names it ("Max").
	Plan string
	// RateLimitedUntil is set when Anthropic asked to wait: the sign-in
	// was read, but usage could not be checked yet.
	RateLimitedUntil time.Time
}

// ErrNoWindows means the sign-in works but the account has no plan windows.
var ErrNoWindows = errors.New("signed in to Claude Code, but Anthropic reports no plan windows for it")

// Check reads Claude Code's sign-in and asks Anthropic for its windows.
func Check(ctx context.Context, o Options) (Result, error) {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	creds, err := claudecodeoauth.ReadFirst(ctx, claudeoauthapi.Stores(o.Home, o.Keychain))
	if err != nil {
		return Result{}, err
	}
	if creds.Expired(o.Now) {
		return Result{}, claudecodeoauth.ErrExpired
	}
	r := Result{Plan: title(creds.SubscriptionType)}
	client := claudeoauthapi.Client{BaseURL: o.BaseURL, UserAgent: o.UserAgent}
	usage, err := client.Usage(ctx, creds.AccessToken, o.Now)
	var limited *claudecodeoauth.RateLimitedError
	switch {
	case errors.As(err, &limited):
		r.RateLimitedUntil = limited.Until
		return r, nil
	case err != nil:
		return Result{}, err
	case !usage.HasSignal():
		return Result{}, ErrNoWindows
	}
	r.Summary = usage.Summary()
	return r, nil
}

func title(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

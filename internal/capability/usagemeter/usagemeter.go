// Package usagemeter connects claude.ai's usage meter: Anthropic's own
// reading of a Claude subscription's windows. It reads the session from a
// browser the operator is signed in with, verifies it with Anthropic, and
// writes what the daemon needs to keep reading it.
//
// `tokenops vendor-usage setup claude-subscription` and the MCP setup
// tool both connect through here. The MCP tool read only the session key
// from the browser: it verified without the bot-check clearance that
// travels with it, and stored the meter as pasted, so the daemon did not
// keep reading the browser whose clearance expires within hours.
package usagemeter

import (
	"context"
	"os"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
	"go.klarlabs.de/tokenops/internal/infra/browsercookie"
	claudeai "go.klarlabs.de/tokenops/internal/infra/vendorusage/claudeusagemeter"
)

// Session is a claude.ai session and what travels with it.
type Session struct {
	Key string
	// Clearance is Cloudflare's cf_clearance cookie, bound to UserAgent:
	// the browser's proof of having passed claude.ai's bot check.
	Clearance, UserAgent string
	// BrowserHeaders and BrowserCookies come from a copied request.
	BrowserHeaders, BrowserCookies map[string]string
	// Browser names the browser the session was read from; empty when
	// it was pasted.
	Browser string
	// OrgID is the organization a copied request named.
	OrgID string
}

// Connection is a session Anthropic accepted and the organization it
// meters.
type Connection = claudeusagemeter.Connection

// Errors a caller can explain.
var (
	ErrBotCheck       = claudeusagemeter.ErrBotCheck
	ErrUnauthorized   = claudeusagemeter.ErrUnauthorized
	ErrNothingToMeter = claudeusagemeter.ErrNothingToMeter
	ErrNotFound       = browsercookie.ErrNotFound
)

// FromBrowser reads the claude.ai session, with its clearance and the
// browser's user agent, from a local browser: only, when set, or the
// first that holds one. A positive keychainWait lets macOS ask the
// operator to allow the browser's Keychain item, waiting that long; zero
// reads quietly, never asking. keychainDisabled reads no Keychain at all,
// so only a browser that does not need it (Firefox) can answer.
func FromBrowser(ctx context.Context, only string, keychainWait time.Duration, keychainDisabled bool) (Session, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Session{}, err
	}
	secret := browsercookie.QuietSecret()
	switch {
	case keychainDisabled:
		secret = browsercookie.DisabledSecret()
	case keychainWait > 0:
		secret = browsercookie.KeychainSecret(keychainWait)
	}
	cookies, b, err := browsercookie.FindMany(ctx, home, "claude.ai", []string{"sessionKey", "cf_clearance"}, only, secret)
	if err != nil {
		return Session{}, err
	}
	return Session{
		Key:       strings.TrimSpace(cookies["sessionKey"]),
		Clearance: strings.TrimSpace(cookies["cf_clearance"]),
		UserAgent: b.UserAgent(),
		Browser:   b.Name,
	}, nil
}

// Verify asks Anthropic whether s works and which organization it
// meters: org (a UUID or name) when set, else s's own, else the first
// that reports usage. baseURL replaces claude.ai's address in tests.
func Verify(ctx context.Context, s Session, org, baseURL string) (Connection, error) {
	client := claudeai.NewClient(s.Key)
	client.Clearance, client.UserAgent = s.Clearance, s.UserAgent
	client.BrowserHeaders, client.BrowserCookies = s.BrowserHeaders, s.BrowserCookies
	if baseURL != "" {
		client.BaseURL = baseURL
	}
	if org == "" {
		org = s.OrgID
	}
	return claudeusagemeter.Connect(ctx, client, org)
}

// Apply enables the meter in cfg for s and conn. A session read from a
// browser stays read from it: the clearance that got past the bot check
// expires within hours, so a stored copy would work today and be refused
// tomorrow.
func Apply(cfg *config.Config, s Session, conn Connection) {
	m := &cfg.VendorUsage.ClaudeUsageMeter
	m.Enabled = true
	m.SessionKey = s.Key
	m.Clearance = s.Clearance
	m.UserAgent = s.UserAgent
	m.BrowserHeaders = s.BrowserHeaders
	m.BrowserCookies = s.BrowserCookies
	m.OrgID = conn.Org.UUID
	m.FromBrowser = s.Browser != ""
	if s.Browser != "" {
		m.Browser = s.Browser
	}
}

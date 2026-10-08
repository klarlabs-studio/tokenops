// Package providersetup is `tokenops vendor-usage setup <provider>` for
// every provider in the registry whose account reader reads with an API key
// or a browser session: it finds the credential, proves it with one reading
// before anything is written, and stores it the way the claude.ai session
// is stored, in the config file. A provider needs no CLI code of its own.
//
// A browser session is read from the browser only here, in a command the
// operator runs, which may show the macOS Keychain prompt. The daemon then
// re-reads it quietly (internal/bootstrap) and never prompts.
package providersetup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/infra/browsercookie"
	accountsapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/accounts"
)

// ErrRefused is a credential the vendor refused.
var ErrRefused = usage.ErrAuth

// ErrNotFound is no session in any local browser.
var ErrNotFound = browsercookie.ErrNotFound

// Provider is a provider setup can connect.
type Provider struct {
	ID   string
	Name string
	// Browser is true for a provider read with a browser session, whose
	// cookies are CookieNames on CookieHost; false for an API key.
	Browser     bool
	CookieHost  string
	CookieNames []string
	// EnvVars are where a key is found without setup.
	EnvVars []string
	// KeyFormat is the credential's shape when it is more than one key
	// ("TEAM_ID:MANAGEMENT_KEY"); empty for a plain API key.
	KeyFormat string
	// Scope says what the reader's optional scope is ("a Kilo
	// organisation ID"); empty for a reader that takes none.
	Scope string
}

// Lookup returns the provider setup connects for id.
func Lookup(id string) (Provider, bool) {
	d, ok := providers.Lookup(strings.ToLower(strings.TrimSpace(id)))
	if !ok {
		return Provider{}, false
	}
	s, ok := d.Setupable()
	if !ok {
		return Provider{}, false
	}
	env := d.EnvVars
	if len(s.EnvVars) > 0 {
		// The source's own variables: the provider's inference keys are
		// not what its reader takes.
		env = s.EnvVars
	}
	p := Provider{ID: string(d.ID), Name: d.DisplayName, EnvVars: env, KeyFormat: s.KeyFormat, Scope: s.Scope}
	if s.Credential == providers.BrowserCookie && s.Cookie != nil {
		p.Browser, p.CookieHost, p.CookieNames = true, s.Cookie.Host, s.Cookie.Names
	}
	return p, true
}

// IDs lists every provider setup connects, for help text.
func IDs() []string {
	var out []string
	for _, d := range providers.All() {
		if _, ok := d.Setupable(); ok {
			out = append(out, string(d.ID))
		}
	}
	return out
}

// FromBrowser reads p's session from a local browser, only from browser
// when it is set. keychainWait > 0 lets macOS ask the operator to allow the
// read, for that long; keychainDisabled reads no Keychain at all. It
// returns the session as a Cookie header value and the browser's name.
func FromBrowser(ctx context.Context, p Provider, browser string, keychainWait time.Duration, keychainDisabled bool) (string, string, error) {
	if !p.Browser {
		return "", "", fmt.Errorf("%s is read with an API key, not a browser session", p.Name)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	secret := browsercookie.QuietSecret()
	switch {
	case keychainDisabled:
		secret = browsercookie.DisabledSecret()
	case keychainWait > 0:
		secret = browsercookie.KeychainSecret(keychainWait)
	}
	cookies, b, err := browsercookie.FindMany(ctx, home, p.CookieHost, p.CookieNames, browser, secret)
	if err != nil {
		return "", "", err
	}
	return browsercookie.Header(cookies, p.CookieNames), b.Name, nil
}

// Verify reads p's account once with key and summarises what the vendor
// reported. Nothing is stored.
// A scope, for a reader that takes one, is read instead of the key's
// default.
func Verify(ctx context.Context, id, key, scope string) ([]string, error) {
	return VerifyWith(ctx, accountsapi.Readers(), id, key, scope)
}

// VerifyWith is Verify with the readers given.
func VerifyWith(ctx context.Context, readers []usage.Reader, id, key, scope string) ([]string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("no credential entered")
	}
	for _, r := range usage.WithScopes(readers, map[string]string{id: scope}) {
		if string(r.Provider()) != id {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		reading, err := r.Read(ctx, key)
		if err != nil {
			return nil, err
		}
		return Summary(reading), nil
	}
	return nil, fmt.Errorf("no account reader for %q", id)
}

// Summary words a reading, one line per figure.
func Summary(r usage.Reading) []string {
	var out []string
	for _, w := range r.Windows {
		line := fmt.Sprintf("%s window: %.0f%% used", w.Name, w.UsedPct)
		if !w.ResetsAt.IsZero() {
			line += ", resets " + w.ResetsAt.Local().Format("Mon 15:04")
		}
		out = append(out, line)
	}
	if r.HasUsed {
		line := fmt.Sprintf("spend: $%.2f", r.UsedUSD)
		if r.LimitUSD > 0 {
			line += fmt.Sprintf(" of $%.2f", r.LimitUSD)
		}
		if days := int(r.UsedPeriod / (24 * time.Hour)); days > 0 {
			line += fmt.Sprintf(" over the last %d days", days)
		}
		out = append(out, line)
	}
	if r.HasBalance {
		out = append(out, fmt.Sprintf("balance: $%.2f", r.BalanceUSD))
	}
	if r.HasCredits {
		out = append(out, fmt.Sprintf("balance: %s %s", strconv.FormatFloat(r.Credits, 'f', -1, 64), r.CreditsUnit))
	}
	if r.HasCreditsUsed {
		line := fmt.Sprintf("spend: %s %s", strconv.FormatFloat(r.CreditsUsed, 'f', -1, 64), r.CreditsUnit)
		if days := int(r.UsedPeriod / (24 * time.Hour)); days > 0 {
			line += fmt.Sprintf(" over the last %d days", days)
		}
		out = append(out, line)
	}
	if r.LimitReached {
		out = append(out, "the vendor reports its limit reached")
	}
	if len(out) == 0 {
		out = append(out, "the account answered, with no plan, spend or balance to show yet")
	}
	return out
}

// CheckScope reports whether scope can be set for p: only a reader that
// takes a scope accepts one, and a scope is one line of printable text.
func CheckScope(p Provider, scope string) error {
	scope = strings.TrimSpace(scope)
	switch {
	case scope == "":
		return nil
	case p.Scope == "":
		return fmt.Errorf("%s takes no scope", p.Name)
	case len(scope) > 200 || strings.ContainsFunc(scope, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return fmt.Errorf("%s's scope must be one line of at most 200 characters", p.Name)
	}
	return nil
}

// ApplyScope stores the scope id's reader reads; "" removes it, so the
// key's default scope is read.
func ApplyScope(cfg *config.Config, id, scope string) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		delete(cfg.VendorUsage.Accounts.Scopes, id)
		return
	}
	if cfg.VendorUsage.Accounts.Scopes == nil {
		cfg.VendorUsage.Accounts.Scopes = map[string]string{}
	}
	cfg.VendorUsage.Accounts.Scopes[id] = scope
}

// Apply stores the credential for id in cfg: the key, and for a session
// read from a browser, that the daemon may re-read it from that browser.
func Apply(cfg *config.Config, id, key string, fromBrowser bool, browser string) {
	if cfg.VendorUsage.Accounts.Credentials == nil {
		cfg.VendorUsage.Accounts.Credentials = map[string]config.AccountCredential{}
	}
	cfg.VendorUsage.Accounts.Credentials[id] = config.AccountCredential{
		Key: strings.TrimSpace(key), FromBrowser: fromBrowser, Browser: browser,
	}
}

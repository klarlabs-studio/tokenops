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
	// Prompt is what to paste when the credential is not an API key.
	Prompt string
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
	p := Provider{ID: string(d.ID), Name: d.DisplayName, EnvVars: d.EnvVars, Prompt: s.Prompt}
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
func Verify(ctx context.Context, id, key string) ([]string, error) {
	return VerifyWith(ctx, accountsapi.Readers(), id, key)
}

// VerifyWith is Verify with the readers given.
func VerifyWith(ctx context.Context, readers []usage.Reader, id, key string) ([]string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("no credential entered")
	}
	for _, r := range readers {
		if string(r.Provider()) != id || usage.IsKeyless(r) {
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
		out = append(out, line)
	}
	if r.HasBalance {
		out = append(out, fmt.Sprintf("balance: $%.2f", r.BalanceUSD))
	}
	if r.LimitReached {
		out = append(out, "the vendor reports its limit reached")
	}
	if len(out) == 0 {
		out = append(out, "the account answered, with no plan, spend or balance to show yet")
	}
	return out
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

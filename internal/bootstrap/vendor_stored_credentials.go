package bootstrap

import (
	"context"
	"os"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/infra/browsercookie"
	accountsapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/accounts"
)

// storedOrigin names a credential `tokenops vendor-usage setup <provider>`
// stored, for status; never the key.
const storedOrigin = "tokenops vendor-usage setup"

// browserCookies reads a provider's session cookies from a browser. The
// daemon's is always quiet (browsercookie.QuietSecret): it never shows a
// Keychain prompt, and when macOS would ask, the read fails and the
// source's health says so.
type browserCookies func(ctx context.Context, host string, names []string, browser string) (map[string]string, error)

// quietBrowser is the daemon's browser read: quiet, or none at all with
// keychain.disabled (only a browser that needs no Keychain, Firefox, can
// then be read).
func quietBrowser(keychainDisabled bool) browserCookies {
	secret := browsercookie.QuietSecret()
	if keychainDisabled {
		secret = browsercookie.DisabledSecret()
	}
	return func(ctx context.Context, host string, names []string, browser string) (map[string]string, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		cookies, _, err := browsercookie.FindMany(ctx, home, host, names, browser, secret)
		return cookies, err
	}
}

// storedCredentials are the credentials setup stored in config, in provider
// order: each stored key, then, for a session read from a browser, a
// credential that re-reads the browser only when the stored one is refused.
func storedCredentials(cfg config.Config, readers []accounts.Reader, cookieOf func(id string) *providers.Cookie, browser browserCookies) []accounts.Credential {
	stored := cfg.VendorUsage.Accounts.Credentials
	ids := make([]string, 0, len(stored))
	for id := range stored {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []accounts.Credential
	for _, id := range ids {
		c := stored[id]
		endpoint := readerEndpoint(readers, id)
		if endpoint == "" {
			continue
		}
		if key := strings.TrimSpace(c.Key); key != "" {
			out = append(out, accounts.Credential{Endpoint: endpoint, Origin: storedOrigin, Key: key})
		}
		spec := cookieOf(id)
		if !c.FromBrowser || spec == nil || browser == nil || strings.EqualFold(c.Browser, config.BrowserNone) {
			continue
		}
		cookie, only := *spec, c.Browser
		out = append(out, accounts.Credential{Endpoint: endpoint, Origin: storedOrigin + " (browser)",
			Resolve: func(ctx context.Context) (string, error) {
				cookies, err := browser(ctx, cookie.Host, cookie.Names, only)
				if err != nil {
					return "", err
				}
				return browsercookie.Header(cookies, cookie.Names), nil
			}})
	}
	return out
}

// registryCookie is the session cookies provider id's setup source reads,
// nil for a provider read with a key.
func registryCookie(id string) *providers.Cookie {
	d, _ := providers.Lookup(id)
	if s, ok := d.Setupable(); ok && s.Credential == providers.BrowserCookie {
		return s.Cookie
	}
	return nil
}

// readerEndpoint is the endpoint whose keys provider id's account reader
// uses.
func readerEndpoint(readers []accounts.Reader, id string) string {
	for _, r := range readers {
		if string(r.Provider()) == id {
			return r.Endpoint()
		}
	}
	return ""
}

// vendorAccountCredentials is every credential the account poller tries on
// a scan: those setup stored first, then the keys the harnesses use.
func vendorAccountCredentials(cfg config.Config) func() []accounts.Credential {
	readers := accountsapi.Readers()
	browser := quietBrowser(cfg.Keychain.Disabled)
	return func() []accounts.Credential {
		return append(storedCredentials(cfg, readers, registryCookie, browser), accountCredentials()...)
	}
}

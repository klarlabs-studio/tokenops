package bootstrap

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
	"go.klarlabs.de/tokenops/internal/infra/browsercookie"
	accountsapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/accounts"
)

// storedOrigin names a credential `tokenops vendor-usage setup <provider>`
// stored, for status; never the key.
const storedOrigin = "tokenops vendor-usage setup"

// browserCookies reads a provider's session from a browser and returns it
// as a Cookie header value. The daemon's is always quiet
// (browsercookie.QuietSecret): it never shows a Keychain prompt, and when
// macOS would ask, the read fails and the source's health says so.
type browserCookies func(ctx context.Context, cookie providers.Cookie, browser string) (string, error)

// quietBrowser is the daemon's browser read: quiet, or none at all with
// keychain.disabled (only a browser that needs no Keychain, Firefox, can
// then be read).
func quietBrowser(keychainDisabled bool) browserCookies {
	secret := browsercookie.QuietSecret()
	if keychainDisabled {
		secret = browsercookie.DisabledSecret()
	}
	return func(ctx context.Context, cookie providers.Cookie, browser string) (string, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		found, err := browsercookie.FindSession(ctx, home, sessionOf(cookie), browser, secret)
		if err != nil {
			return "", err
		}
		return found.Header(), nil
	}
}

// sessionOf is the browser session a descriptor's cookie spec reads.
func sessionOf(c providers.Cookie) browsercookie.Session {
	return browsercookie.Session{Hosts: c.Hosts(), Names: c.Names, Proof: c.Proof, AllForHost: c.AllForHost}
}

// setupAgain is the remedy for a refused stored credential: only the
// operator, in setup, may read a browser with a prompt or sign in again.
func setupAgain(id string) string {
	return "the stored credential expired or was refused, and TokenOps never prompts in the background: run `tokenops vendor-usage setup " + id + "` again"
}

// storedCredentials are the credentials setup stored in config, in provider
// order: each stored key, then, for a session read from a browser, a
// credential that re-reads the browser only when the stored one is refused.
// A refusal of either says to run setup again.
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
		if c.BaseURL != "" {
			// A gateway: read at the address setup stored, by that
			// gateway only.
			if key := strings.TrimSpace(c.Key); key != "" {
				out = append(out, accounts.Credential{Endpoint: accounts.GatewayEndpoint, Origin: storedOrigin,
					Key: key, BaseURL: c.BaseURL, Gateway: id, Remedy: setupAgain(id)})
			}
			continue
		}
		endpoint := readerEndpoint(readers, id)
		if endpoint == "" {
			continue
		}
		if c.CredentialChain {
			if chain := chainReader(readers, id); chain != nil {
				out = append(out, accounts.Credential{Endpoint: endpoint, Origin: storedOrigin + " (credential chain)",
					Remedy: setupAgain(id), Resolve: func(ctx context.Context) (string, error) {
						key, _, err := chain.Chain(ctx)
						return key, err
					}})
			}
			continue
		}
		if key := strings.TrimSpace(c.Key); key != "" {
			out = append(out, accounts.Credential{Endpoint: endpoint, Origin: storedOrigin, Key: key, Remedy: setupAgain(id)})
		}
		spec := cookieOf(id)
		if !c.FromBrowser || spec == nil || spec.PasteOnly || browser == nil || strings.EqualFold(c.Browser, config.BrowserNone) {
			continue
		}
		cookie, only := *spec, c.Browser
		out = append(out, accounts.Credential{Endpoint: endpoint, Origin: storedOrigin + " (browser)", Remedy: setupAgain(id),
			Resolve: func(ctx context.Context) (string, error) {
				header, err := browser(ctx, cookie, only)
				if err != nil {
					return "", fmt.Errorf("%w (re-reading the session quietly: %v)", accounts.ErrAuth, err)
				}
				return header, nil
			}})
	}
	return out
}

// registryCookie is the session cookies provider id's browser-session
// source reads, nil for a provider read only with a key.
func registryCookie(id string) *providers.Cookie {
	d, _ := providers.Lookup(id)
	if s, ok := d.SessionSource(); ok {
		return s.Cookie
	}
	return nil
}

// chainReader is provider id's reader when it reads with the vendor's
// credential chain.
func chainReader(readers []accounts.Reader, id string) accounts.ChainReader {
	for _, r := range readers {
		if c, ok := r.(accounts.ChainReader); ok && string(r.Provider()) == id {
			return c
		}
	}
	return nil
}

// sourceEndpoint is the endpoint of the account reader that writes source
// tag: a provider may have several readers (Kiro's CLI and its overage).
func sourceEndpoint(readers []accounts.Reader, tag string) string {
	for _, r := range readers {
		if r.Source() == tag {
			return r.Endpoint()
		}
	}
	return ""
}

// readerEndpoint is the endpoint whose keys provider id's account reader
// uses.
func readerEndpoint(readers []accounts.Reader, id string) string {
	for _, r := range readers {
		if string(r.Provider()) == id && !accounts.IsKeyless(r) {
			return r.Endpoint()
		}
	}
	return ""
}

// grantOrigin names a granted sign-in for status; never the token.
const grantOrigin = "another app's sign-in, granted with --use-app-login"

// grantedCredentials are the other applications' sign-ins the operator
// granted (ADR 0013), in provider order. Each is read only when every
// credential before it was refused or absent, afresh each time (so the
// owning application's refreshed token is picked up), read-only, and only
// while the grant still names what the provider's descriptor reads. A
// provider with no grant has nothing here: nothing it owns is read.
func grantedCredentials(cfg config.Config, readers []accounts.Reader, env applogin.Env) []accounts.Credential {
	ids := make([]string, 0, len(cfg.VendorUsage.Grants))
	for id := range cfg.VendorUsage.Grants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []accounts.Credential
	for _, id := range ids {
		g := cfg.VendorUsage.Grants[id]
		d, ok := providers.Lookup(id)
		if !ok {
			continue
		}
		s, ok := d.AppLoginSource()
		if !ok {
			continue
		}
		endpoint := sourceEndpoint(readers, s.Tag)
		if endpoint == "" {
			continue
		}
		located, ok := applogin.Granted(s.AppLogins, g.Kind, g.Item, g.Fields, g.Host, g.FromEnv, env)
		if !ok {
			continue
		}
		out = append(out, accounts.Credential{Endpoint: endpoint, Origin: grantOrigin, AppLogin: true,
			Resolve: func(ctx context.Context) (string, error) {
				return applogin.Read(ctx, located, env)
			}})
	}
	return out
}

// vendorAccountCredentials is every credential the account poller tries on
// a scan: those setup stored first, then the keys the harnesses use, then,
// last, the other applications' sign-ins the operator granted.
func vendorAccountCredentials(cfg config.Config) func() []accounts.Credential {
	readers := accountsapi.Readers()
	browser := quietBrowser(cfg.Keychain.Disabled)
	env := applogin.Env{KeychainDisabled: cfg.Keychain.Disabled}
	return func() []accounts.Credential {
		out := append(storedCredentials(cfg, readers, registryCookie, browser), accountCredentials()...)
		return append(out, grantedCredentials(cfg, readers, env)...)
	}
}

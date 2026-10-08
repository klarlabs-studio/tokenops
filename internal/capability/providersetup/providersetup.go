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
	"go.klarlabs.de/tokenops/internal/infra/keychain"
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
	// cookies Cookie names. PasteOnly sessions are not read from a browser:
	// setup asks for the Cookie header.
	Browser bool
	Cookie  providers.Cookie
	// Key is true when the provider is also (or only) read with an API key.
	Key bool
	// Login is true for a provider whose session comes from a username and
	// password sign-in; setup stores only the session token it returns.
	Login bool
	// Gateway is true for a gateway read at an address the operator
	// gives; DefaultBaseURL is the hosted service's, when it has one, and
	// BaseURLEnv the variable that names it without setup.
	Gateway        bool
	DefaultBaseURL string
	BaseURLEnv     string
	// Chain is true for a provider read with the vendor's own credential
	// chain on this machine: setup finds it, proves it and opts in, and
	// stores nothing secret.
	Chain bool
	// EnvVars are where a key is found without setup.
	EnvVars []string
	// KeyFormat is the credential's shape when it is more than one key
	// ("TEAM_ID:MANAGEMENT_KEY"); empty for a plain API key.
	KeyFormat string
	// KeychainServer is set for a provider read with another app's
	// sign-in from the Keychain: the internet password's server.
	KeychainServer string
}

// FromKeychain reads p's sign-in from the macOS Keychain, letting macOS
// ask the operator to allow it for at most wait. It returns the credential
// as the reader takes it, "<account> <secret>". Only setup calls it; a
// keychainDisabled config reads nothing.
func FromKeychain(ctx context.Context, p Provider, wait time.Duration, keychainDisabled bool) (string, error) {
	switch {
	case p.KeychainServer == "":
		return "", fmt.Errorf("%s is not read from the Keychain", p.Name)
	case keychainDisabled:
		return "", keychain.ErrDisabled
	}
	l, err := keychain.PromptingInternet(ctx, p.KeychainServer, wait)
	if err != nil {
		return "", err
	}
	return l.Account + " " + l.Secret, nil
}

// CookieHosts names the hosts a browser session is read for.
func (p Provider) CookieHosts() string { return strings.Join(p.Cookie.Hosts(), " or ") }

// CookieNames names the cookies a browser session is read with.
func (p Provider) CookieNames() string {
	if p.Cookie.AllForHost && len(p.Cookie.Names) == 0 {
		return "the cookies your browser sends there"
	}
	return strings.Join(p.Cookie.Names, ", ")
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
	p := Provider{ID: string(d.ID), Name: d.DisplayName, KeyFormat: s.KeyFormat,
		EnvVars: append(append([]string(nil), d.EnvVars...), s.EnvVars...)}
	p.Chain = s.Credential == providers.CredentialChain
	if s.Reader == providers.GatewayReader {
		p.Gateway, p.DefaultBaseURL, p.BaseURLEnv = true, s.DefaultBaseURL, s.BaseURLEnv
	}
	if s.Credential == providers.AppKeychain {
		p.KeychainServer = s.KeychainServer
	}
	for _, src := range d.Sources {
		if src.Reader != providers.AccountReader {
			continue
		}
		switch src.Credential {
		case providers.APIKey:
			p.Key = true
		case providers.PasswordLogin:
			p.Login = true
		case providers.BrowserCookie:
			if src.Cookie != nil && !p.Browser {
				p.Browser, p.Cookie = true, *src.Cookie
			}
		}
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
	if !p.Browser || p.Cookie.PasteOnly {
		return "", "", fmt.Errorf("%s's session is not read from a browser", p.Name)
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
	session := browsercookie.Session{Hosts: p.Cookie.Hosts(), Names: p.Cookie.Names, Proof: p.Cookie.Proof, AllForHost: p.Cookie.AllForHost}
	found, err := browsercookie.FindSession(ctx, home, session, browser, secret)
	if err != nil {
		return "", "", err
	}
	return found.Header(), found.Browser.Name, nil
}

// Login signs in to provider id with a username and password and returns
// the session token the vendor issued. The password is sent only to the
// vendor's sign-in endpoint and is neither stored nor logged.
func Login(ctx context.Context, id, username, password string) (string, error) {
	return LoginWith(ctx, accountsapi.Readers(), id, username, password)
}

// LoginWith is Login with the readers given.
func LoginWith(ctx context.Context, readers []usage.Reader, id, username, password string) (string, error) {
	if strings.TrimSpace(username) == "" || password == "" {
		return "", errors.New("a username and a password are needed")
	}
	for _, r := range readers {
		l, ok := r.(accountsapi.PasswordLogin)
		if !ok || string(r.Provider()) != id {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return l.Login(ctx, strings.TrimSpace(username), password)
	}
	return "", fmt.Errorf("%s has no password sign-in", id)
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	found := false
	for _, r := range readers {
		if string(r.Provider()) != id || usage.IsKeyless(r) {
			continue
		}
		found = true
		reading, err := r.Read(ctx, key)
		if errors.Is(err, usage.ErrSkip) {
			// Another of the provider's readers reads this kind of
			// credential (an API key, not a session, or the other way).
			continue
		}
		if err != nil {
			return nil, err
		}
		return Summary(reading), nil
	}
	if found {
		return nil, fmt.Errorf("%s reads neither an API key nor a session of that form", id)
	}
	return nil, fmt.Errorf("no account reader for %q", id)
}

// VerifyGateway reads gateway id's budget once with key at base, the
// address the operator gave, and summarises it. Nothing is stored.
func VerifyGateway(ctx context.Context, id, base, key string) ([]string, error) {
	return VerifyGatewayWith(ctx, accountsapi.Gateways(), id, base, key)
}

// VerifyGatewayWith is VerifyGateway with the gateways given.
func VerifyGatewayWith(ctx context.Context, gateways []usage.Gateway, id, base, key string) ([]string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("no credential entered")
	}
	root, ok := usage.NamedGatewayBase(base)
	if !ok {
		return nil, fmt.Errorf("%q is not an address a key may be sent to: use https, or http only for a local or private-network host", base)
	}
	for _, g := range gateways {
		if g.Name() != id {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		reading, err := g.Read(ctx, root, key)
		if err != nil {
			return nil, err
		}
		return Summary(reading), nil
	}
	return nil, fmt.Errorf("no gateway reader for %q", id)
}

// FromChain finds provider id's credential in the vendor's own chain on
// this machine and says where it was found; the credential is for Verify
// only and is never stored.
func FromChain(ctx context.Context, id string) (key, origin string, err error) {
	return FromChainWith(ctx, accountsapi.Readers(), id)
}

// FromChainWith is FromChain with the readers given.
func FromChainWith(ctx context.Context, readers []usage.Reader, id string) (string, string, error) {
	for _, r := range readers {
		if c, ok := r.(usage.ChainReader); ok && string(r.Provider()) == id {
			return c.Chain(ctx)
		}
	}
	return "", "", fmt.Errorf("%q is not read with a credential chain", id)
}

// ApplyChain opts provider id in to being read with its vendor's
// credential chain as the daemon polls; nothing secret is stored.
func ApplyChain(cfg *config.Config, id string) {
	if cfg.VendorUsage.Accounts.Credentials == nil {
		cfg.VendorUsage.Accounts.Credentials = map[string]config.AccountCredential{}
	}
	cfg.VendorUsage.Accounts.Credentials[id] = config.AccountCredential{CredentialChain: true}
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

// ApplyGateway stores gateway id's key and the address it is read at.
func ApplyGateway(cfg *config.Config, id, base, key string) {
	if cfg.VendorUsage.Accounts.Credentials == nil {
		cfg.VendorUsage.Accounts.Credentials = map[string]config.AccountCredential{}
	}
	cfg.VendorUsage.Accounts.Credentials[id] = config.AccountCredential{
		Key: strings.TrimSpace(key), BaseURL: strings.TrimSpace(base),
	}
}

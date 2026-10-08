// Package accounts reads gateway and API vendors' own account figures,
// with the key the operator's harness already sends them (ADR 0009 §7):
// a key's spend and cap on OpenRouter, the prepaid balance on DeepSeek and
// Moonshot. Each reader calls only its vendor's documented endpoint, with a
// key found for that vendor's endpoint; keys are never stored or logged.
//
// The package holds the readings, the Reader and Gateway ports, the poller
// that decides which key goes where, and the mapping to envelopes. The
// HTTP readers live in internal/infra/vendorusage/accounts.
package accounts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// ErrAuth reports a key the vendor refused.
var ErrAuth = errors.New("accounts: the key was refused")

// ErrNotInstalled reports a Keyless reader whose vendor CLI or app is not
// on this machine: there is nothing to read, and nothing is wrong.
var ErrNotInstalled = errors.New("accounts: not installed on this machine")

// Keyless is a Reader that needs no key: the vendor's own CLI signs its own
// request, or the vendor's app keeps its figures in a file on disk (ADR
// 0011 §1, the vendor's own records). The poller reads it on every scan
// with an empty key and never hands it a credential; it answers
// ErrNotInstalled when its CLI or file is absent.
type Keyless interface {
	Reader
	// Keyless marks the reader; it does nothing.
	Keyless()
}

// IsKeyless reports whether r reads without a key.
func IsKeyless(r Reader) bool {
	_, ok := r.(Keyless)
	return ok
}

// ErrSkip is a reader declining a credential that is not of the kind it
// reads (a browser session handed to the reader of the vendor's API key,
// or the other way round) without calling the vendor. The credential does
// not count as tried.
var ErrSkip = errors.New("accounts: not a credential this reader reads")

// KeyOnly is implemented by a reader that reads only API keys: a
// credential that would re-read a browser session is never resolved for
// it.
type KeyOnly interface{ KeyOnly() bool }

// Credential is a key and the endpoint it was found for.
type Credential struct {
	// Endpoint is the endpoint name (biller's), e.g. "openrouter".
	Endpoint string
	// Origin says where it was found, for status; never the key.
	Origin string
	Key    string
	// Resolve finds the key when it is needed, for a credential that is
	// costly or intrusive to read (a browser session): it is called only
	// when Key is empty and no credential before it was accepted.
	Resolve func(context.Context) (string, error)
	// BaseURL is where the harness sends the key. A gateway credential
	// is read there and nowhere else.
	BaseURL string
	// Gateway names the gateway a credential was set up or configured for
	// (`tokenops vendor-usage setup sub2api`, SUB2API_BASE_URL): it is read
	// at BaseURL by that gateway's reader without being recognised first,
	// for a gateway with no route that names it without a key.
	Gateway string
	// Remedy is what the operator does when the vendor refuses this
	// credential ("run `tokenops vendor-usage setup acme` again"); it is
	// added to the source's health error. Never the key.
	Remedy string
	// AppLogin marks another application's sign-in the operator granted
	// (ADR 0013): Resolve reads it afresh, and a reader that reads such a
	// token differently from an API key (AppLoginReader) is given it
	// through ReadAppLogin.
	AppLogin bool
}

// AppLoginReader is a Reader that reads with another application's
// sign-in in a different form from its API key: a session token sent as a
// cookie, several fields as a JSON object. A reader without it is given the
// token through Read, as a key.
type AppLoginReader interface {
	ReadAppLogin(ctx context.Context, token string) (Reading, error)
}

// Reading is what a vendor reports about the account.
type Reading struct {
	// Scope names what the figures cover: "key", "account".
	Scope string
	// UsedUSD is spend in the period, when the vendor reports it.
	UsedUSD float64
	HasUsed bool
	// LimitUSD is the cap UsedUSD is spent against, 0 for none.
	LimitUSD float64
	// BalanceUSD is prepaid credit left, when the vendor reports it.
	BalanceUSD float64
	HasBalance bool
	// Credits is prepaid credit left in the vendor's own unit, CreditsUnit
	// ("points"), when the vendor reports no dollar figure. It is stored
	// as reported and never converted to dollars.
	Credits     float64
	CreditsUnit string
	HasCredits  bool
	// LimitReached is the vendor saying requests are blocked.
	LimitReached bool
	// Subscription marks an account on a plan rather than billed per
	// token; its Windows are the plan's usage windows.
	Subscription bool
	// Windows are usage windows as the vendor reports them.
	Windows []Window
	// Counts are usage the vendor reports as a count with no allowance to
	// measure it against (CodeRabbit's reviews this billing period), so no
	// percentage, and no window.
	Counts []Count
}

// Count is a count of use in a period, with no allowance.
type Count struct {
	// Name is what is counted: "reviews".
	Name string
	Used float64
	// ResetsAt is when the period's count starts again, when known.
	ResetsAt time.Time
}

// Empty reports whether the reading says nothing worth storing: an
// account with no plan windows, no spend and no balance (a key on a
// vendor's free tier, say).
func (r Reading) Empty() bool {
	return len(r.Windows) == 0 && len(r.Counts) == 0 && !r.HasUsed && !r.HasBalance && !r.HasCredits && r.LimitUSD == 0 && !r.LimitReached
}

// Window is one usage window: the share used and when it resets.
type Window struct {
	// Name is the window in words: "5h", "week", "month".
	Name     string
	UsedPct  float64
	Duration time.Duration
	ResetsAt time.Time
}

// Reader reads one vendor's account.
type Reader interface {
	// Endpoint is the endpoint whose keys this reader uses.
	Endpoint() string
	// Provider is the biller the reading is for.
	Provider() eventschema.Provider
	// Source is the event source tag.
	Source() string
	Read(ctx context.Context, key string) (Reading, error)
}

// ChainReader is a Reader whose credential is the vendor's own standard
// credential chain on this machine (AWS's environment and shared
// credentials file), not a key: Chain finds it, and says where, as the key
// Read takes. It is read only for a provider the operator set up.
type ChainReader interface {
	Reader
	Chain(ctx context.Context) (key, origin string, err error)
}

// Paced is a Reader the poller asks no more often than MinInterval: one
// whose vendor bills each request (AWS Cost Explorer) or refreshes its
// figures only a few times a day.
type Paced interface {
	MinInterval() time.Duration
}

// GatewayEndpoint is the endpoint name of a credential whose base URL is a
// host TokenOps does not know: possibly a gateway the operator runs or
// subscribes to. A gateway reader recognises it before reading.
const GatewayEndpoint = "gateway"

// Gateway reads the calling key's own budget on an AI gateway: one the
// operator runs themselves (LiteLLM, Bifrost) or a hosted one
// (ClawRouter). The key is sent only to the base URL the harness already
// sends it to.
type Gateway interface {
	// Name is the gateway, e.g. "litellm"; it is also the provider the
	// reading is reported under.
	Name() string
	Source() string
	// Recognise reports whether root is this gateway, without a key.
	Recognise(ctx context.Context, root string) bool
	Read(ctx context.Context, root, key string) (Reading, error)
}

// gatewayRoot is the scheme and host of a base URL: harnesses point at a
// provider path under it (/anthropic, /v1), while health and budget
// routes hang off the root.
func gatewayRoot(baseURL string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// NamedGatewayBase is the address a named gateway is read at: the base URL
// the operator gave, without a trailing slash or "/v1", since the gateway's
// own routes hang off it. The key is sent there, so it must be HTTPS, or
// plain HTTP only to a loopback, private-network or .local host, and carry
// no credentials, query or fragment.
func NamedGatewayBase(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !privateHost(u.Hostname()) {
			return "", false
		}
	default:
		return "", false
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	path = strings.TrimSuffix(path, "/v1")
	return u.Scheme + "://" + u.Host + strings.TrimRight(path, "/"), true
}

// privateHost reports a host plain HTTP may carry a key to: loopback, a
// private or link-local address, or an mDNS .local name.
func privateHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// gatewayReader adapts a recognised gateway to Reader, for NewEnvelope.
type gatewayReader struct{ g Gateway }

func (r gatewayReader) Endpoint() string               { return GatewayEndpoint }
func (r gatewayReader) Provider() eventschema.Provider { return eventschema.Provider(r.g.Name()) }
func (r gatewayReader) Source() string                 { return r.g.Source() }
func (r gatewayReader) Read(context.Context, string) (Reading, error) {
	return Reading{}, fmt.Errorf("accounts: a gateway is read through its root")
}

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
	"net/url"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// ErrAuth reports a key the vendor refused.
var ErrAuth = errors.New("accounts: the key was refused")

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
}

// Reading is what a vendor reports about the account.
type Reading struct {
	// Scope names what the figures cover: "key", "account".
	Scope string
	// UsedUSD is spend in the period, when the vendor reports it.
	UsedUSD float64
	HasUsed bool
	// UsedPeriod is the trailing period UsedUSD and CreditsUsed cover when
	// the vendor reports spend over the last N days rather than its
	// billing period (xAI's last 30 days); 0 is the billing period.
	UsedPeriod time.Duration
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
	// CreditsUsed is spend in the vendor's own unit, CreditsUnit, over
	// UsedPeriod (Poe's points over the last 30 days), as reported and
	// never converted to dollars.
	CreditsUsed    float64
	HasCreditsUsed bool
	// LimitReached is the vendor saying requests are blocked.
	LimitReached bool
	// Subscription marks an account on a plan rather than billed per
	// token; its Windows are the plan's usage windows.
	Subscription bool
	// Windows are usage windows as the vendor reports them.
	Windows []Window
}

// Empty reports whether the reading says nothing worth storing: an
// account with no plan windows, no spend and no balance (a key on a
// vendor's free tier, say).
func (r Reading) Empty() bool {
	return len(r.Windows) == 0 && !r.HasUsed && !r.HasBalance && !r.HasCredits && !r.HasCreditsUsed && r.LimitUSD == 0 && !r.LimitReached
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

// Scoped is a reader that can read another scope than its key's default:
// a Kilo organisation, a v0 project. The scope is the operator's setting
// (vendor_usage.accounts.scopes.<provider>), passed to the vendor as is.
type Scoped interface {
	Reader
	// WithScope returns the reader reading scope; "" is the default.
	WithScope(scope string) Reader
}

// WithScopes returns readers with each scoped reader set to the scope
// scopes names for its provider. A reader with no scope set, or that takes
// none, is returned as it is.
func WithScopes(readers []Reader, scopes map[string]string) []Reader {
	out := make([]Reader, len(readers))
	for i, r := range readers {
		out[i] = r
		scope := strings.TrimSpace(scopes[string(r.Provider())])
		if s, ok := r.(Scoped); ok && scope != "" {
			out[i] = s.WithScope(scope)
		}
	}
	return out
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

// gatewayReader adapts a recognised gateway to Reader, for NewEnvelope.
type gatewayReader struct{ g Gateway }

func (r gatewayReader) Endpoint() string               { return GatewayEndpoint }
func (r gatewayReader) Provider() eventschema.Provider { return eventschema.Provider(r.g.Name()) }
func (r gatewayReader) Source() string                 { return r.g.Source() }
func (r gatewayReader) Read(context.Context, string) (Reading, error) {
	return Reading{}, fmt.Errorf("accounts: a gateway is read through its root")
}

package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerHyper and readerHyperWeb register the readers (readers_gen.go).
func readerHyper() usage.Reader    { return Hyper{} }
func readerHyperWeb() usage.Reader { return HyperWeb{} }

// Hyper reads the Hypercredits left on Charm Hyper with an API key, from
// GET hyper.charm.land/v1/credits, as CodexBar's Charm Hyper provider does
// (Sources/CodexBarCore/Resources/Plugins/hyper.ts, docs/hyper.md). The
// balance is in Hypercredits and is kept in them.
type Hyper struct {
	BaseURL string
	HTTP    *http.Client
}

func (Hyper) Endpoint() string               { return "hyper" }
func (Hyper) Provider() eventschema.Provider { return "hyper" }
func (Hyper) Source() string                 { return "hyper-account" }
func (Hyper) KeyOnly() bool                  { return true }

func (h Hyper) Read(ctx context.Context, key string) (usage.Reading, error) {
	if isSession(key) {
		return usage.Reading{}, usage.ErrSkip
	}
	var raw json.RawMessage
	if err := getJSON(ctx, h.HTTP, base(h.BaseURL, "https://hyper.charm.land")+"/v1/credits", strings.TrimSpace(key), &raw); err != nil {
		return usage.Reading{}, err
	}
	return hyperReading(raw)
}

// HyperWeb reads the same balance with the hyper.charm.land browser
// session, which the endpoint also takes.
type HyperWeb struct {
	BaseURL string
	HTTP    *http.Client
}

func (HyperWeb) Endpoint() string               { return "hyper" }
func (HyperWeb) Provider() eventschema.Provider { return "hyper" }
func (HyperWeb) Source() string                 { return "hyper-web" }

func (h HyperWeb) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	if !isSession(cookie) {
		return usage.Reading{}, usage.ErrSkip
	}
	body, err := doWeb(ctx, h.HTTP, http.MethodGet, base(h.BaseURL, "https://hyper.charm.land")+"/v1/credits",
		http.Header{"Cookie": {cookie}, "Accept": {"application/json"}}, nil)
	if err != nil {
		return usage.Reading{}, err
	}
	if looksLikeSignIn(body) || strings.HasPrefix(strings.TrimSpace(strings.ToLower(string(body))), "<") {
		return usage.Reading{}, fmt.Errorf("%w (hyper.charm.land answered with a web page: the session is signed out)", usage.ErrAuth)
	}
	return hyperReading(body)
}

// hyperReading is {"balance": n}, a finite number at or above zero.
func hyperReading(body []byte) (usage.Reading, error) {
	var resp struct {
		Balance json.RawMessage `json:"balance"`
	}
	if json.Unmarshal(body, &resp) != nil {
		return usage.Reading{}, errors.New("accounts: GET hyper.charm.land/v1/credits: unexpected shape")
	}
	v, ok := finiteNumber(resp.Balance)
	if !ok || v < 0 {
		return usage.Reading{}, errors.New("accounts: GET hyper.charm.land/v1/credits: unreadable balance")
	}
	return usage.Reading{Scope: "account", Credits: v, CreditsUnit: "Hypercredits", HasCredits: true}, nil
}

package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerSofya registers the reader (readers_gen.go).
func readerSofya() usage.Reader { return Sofya{} }

// Sofya reads GET sofya.co/v1/auth/me (sofya.co/docs), ported from
// CodexBar (Sources/CodexBarCore/Resources/Plugins/sofya.ts,
// Providers/Sofya, docs/sofya.md): the account's available credits, kept
// in credits. The answer has no allowance to measure them against, so no
// window is claimed.
type Sofya struct {
	BaseURL string
	HTTP    *http.Client
}

func (Sofya) Endpoint() string               { return "sofya" }
func (Sofya) Provider() eventschema.Provider { return "sofya" }
func (Sofya) Source() string                 { return "sofya-account" }

func (s Sofya) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp map[string]json.RawMessage
	if err := getJSON(ctx, s.HTTP, base(s.BaseURL, "https://sofya.co")+"/v1/auth/me", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	var available *float64
	seen := false
	for _, k := range []string{"credits", "plan_credits", "purchased_credits"} {
		raw, ok := resp[k]
		if !ok || string(raw) == "null" {
			continue
		}
		var v float64
		if json.Unmarshal(raw, &v) != nil || v < 0 {
			return usage.Reading{}, errors.New("accounts: sofya credits: unrecognised answer")
		}
		seen = true
		if k == "credits" {
			available = &v
		}
	}
	if !seen {
		return usage.Reading{}, errors.New("accounts: sofya credits: unrecognised answer")
	}
	if available == nil {
		return usage.Reading{}, errors.New("accounts: sofya credits: no available credits in the answer")
	}
	return usage.Reading{Scope: "account", Credits: *available, CreditsUnit: "credits", HasCredits: true}, nil
}

package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerClaudePlatform registers the reader (readers_gen.go).
func readerClaudePlatform() usage.Reader { return ClaudePlatform{} }

// ClaudePlatform reads the Claude Console organisation's prepaid API
// credit and its spend this month, ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/claudeplatform.ts,
// Providers/ClaudePlatform, docs/claudeplatform.md): the undocumented
// endpoints platform.claude.com's billing page calls, with the Console's
// sessionKey cookie. Inference API keys cannot read billing, and an admin
// key is not available to an individual Console account, so the session
// is the only credential that reads it.
//
// It is API credit, not a Claude subscription's windows: those come from
// the status line and the claude.ai meter. Its endpoint is its own,
// "claude-platform", so no inference key is ever sent to the Console.
type ClaudePlatform struct {
	BaseURL string
	HTTP    *http.Client
}

func (ClaudePlatform) Endpoint() string               { return "claude-platform" }
func (ClaudePlatform) Provider() eventschema.Provider { return eventschema.ProviderAnthropic }
func (ClaudePlatform) Source() string                 { return "claude-platform" }

// claudeOrgID is the form an organisation's UUID takes; anything else is
// never put in a path.
var claudeOrgID = lazyRegexp(`^[A-Za-z0-9-]{1,64}$`)

type claudePlatformCredits struct {
	Amount   *int64 `json:"amount"`
	Currency string `json:"currency"`
	Balance  *struct {
		Credits *struct {
			Exponent *int `json:"exponent"`
		} `json:"credits"`
	} `json:"balance"`
}

func (c ClaudePlatform) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := claudePlatformCookie(key)
	if cookie == "" {
		return usage.Reading{}, fmt.Errorf("%w (no Claude Console sessionKey in the credential)", usage.ErrAuth)
	}
	host := base(c.BaseURL, "https://platform.claude.com")
	get := func(path string, out any) error {
		body, err := doWeb(ctx, c.HTTP, http.MethodGet, host+path, http.Header{
			"Accept": {"application/json"}, "Cookie": {cookie},
		}, nil)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, out); err != nil {
			return errors.New("accounts: claude platform: unrecognised answer")
		}
		return nil
	}
	var orgs []struct {
		UUID         string   `json:"uuid"`
		Capabilities []string `json:"capabilities"`
	}
	if err := get("/api/organizations", &orgs); err != nil {
		return usage.Reading{}, err
	}
	// Claude.ai chat organisations share the list; only an API
	// organisation owns Console billing. The first whose billing this
	// member can read wins.
	var (
		orgID   string
		credits claudePlatformCredits
		lastErr = fmt.Errorf("%w (this Claude account has no Console API organisation)", usage.ErrAuth)
	)
	for _, o := range orgs {
		if !slices.Contains(o.Capabilities, "api") {
			continue
		}
		if !claudeOrgID().MatchString(o.UUID) {
			return usage.Reading{}, errors.New("accounts: claude platform: unrecognised organisation ID")
		}
		credits = claudePlatformCredits{}
		err := get("/api/organizations/"+o.UUID+"/prepaid/credits", &credits)
		if err == nil {
			orgID = o.UUID
			break
		}
		if !errors.Is(err, usage.ErrAuth) {
			return usage.Reading{}, err
		}
		lastErr = err
	}
	if orgID == "" {
		return usage.Reading{}, lastErr
	}
	scale, currency, err := claudePlatformScale(credits)
	if err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "organization"}
	setMoney(&r, float64(*credits.Amount)/scale, currency)

	// Spend is optional: a failed or malformed report keeps the balance.
	var spend struct {
		Amount *int64 `json:"amount"`
	}
	switch err := get("/api/organizations/"+orgID+"/current_spend", &spend); {
	case err != nil || spend.Amount == nil || *spend.Amount < 0:
		r.Notice = "the Console's spend this month was unavailable"
	case strings.EqualFold(currency, "USD"):
		r.UsedUSD, r.HasUsed = float64(*spend.Amount)/scale, true
	}
	return r, nil
}

// claudePlatformScale is what the minor units are divided by (10^exponent,
// 0–4, 2 when absent) and the currency, defaulting to USD.
func claudePlatformScale(c claudePlatformCredits) (float64, string, error) {
	if c.Amount == nil {
		return 0, "", errors.New("accounts: claude platform: no credit balance in the answer")
	}
	currency := strings.ToUpper(strings.TrimSpace(c.Currency))
	if currency == "" {
		currency = "USD"
	}
	if !currencyCode().MatchString(currency) {
		return 0, "", errors.New("accounts: claude platform: unrecognised currency")
	}
	exp := 2
	if c.Balance != nil && c.Balance.Credits != nil && c.Balance.Credits.Exponent != nil {
		exp = *c.Balance.Credits.Exponent
		if exp < 0 || exp > 4 {
			return 0, "", errors.New("accounts: claude platform: unrecognised currency exponent")
		}
	}
	return math.Pow10(exp), currency, nil
}

// claudePlatformCookie is the sessionKey cookie to send: from a Cookie
// header, or a bare value.
func claudePlatformCookie(credential string) string {
	if v := sessionToken(credential, "sessionKey"); v != "" {
		return "sessionKey=" + v
	}
	return ""
}

// currencyCode is an ISO 4217 code.
var currencyCode = lazyRegexp(`^[A-Z]{3}$`)

// setMoney records a balance: in dollars when it is USD, else in its own
// currency as reported, never converted.
func setMoney(r *usage.Reading, amount float64, currency string) {
	if strings.EqualFold(currency, "USD") {
		r.BalanceUSD, r.HasBalance = amount, true
		return
	}
	r.Credits, r.CreditsUnit, r.HasCredits = amount, strings.ToUpper(currency), true
}

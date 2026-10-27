package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerOllamaAPI registers the reader (readers_gen.go).
func readerOllamaAPI() usage.Reader { return OllamaAPI{} }

// OllamaAPI reads Ollama Cloud's balance with an API key:
// GET https://ollama.com/api/balance, as CodexBar's Ollama API-key source
// does (ollama-api.ts, pull 4401). included is the monthly allowance:
// allowance_usd, and balance_usd is what is left of it, so the month is
// the allowance less the balance; period.until is its reset. purchased
// is credit bought on top, a balance and never part of the allowance.
// It is the first source of Ollama Cloud's figures when a key is set; the
// session scrape of the settings page (Ollama) stays as the other source.
type OllamaAPI struct {
	BaseURL string
	HTTP    *http.Client
}

func (OllamaAPI) Endpoint() string               { return "ollama" }
func (OllamaAPI) Provider() eventschema.Provider { return "ollama" }
func (OllamaAPI) Source() string                 { return "ollama-api" }

// KeyOnly: a session cookie is declined, never sent to the API.
func (OllamaAPI) KeyOnly() bool { return true }

// ollamaUSD is a dollar figure as the API sends it, a JSON number or a
// decimal string. Anything else is an error, not an absent figure.
type ollamaUSD struct{ v float64 }

func (u *ollamaUSD) UnmarshalJSON(b []byte) error {
	raw := strings.TrimSpace(string(b))
	if strings.HasPrefix(raw, `"`) {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		raw = strings.TrimSpace(s)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return errors.New("not a dollar figure")
	}
	u.v = v
	return nil
}

// ollamaBalance is the /api/balance answer.
type ollamaBalance struct {
	Included *struct {
		Allowance *ollamaUSD `json:"allowance_usd"`
		Balance   *ollamaUSD `json:"balance_usd"`
		Period    *struct {
			Until stamp `json:"until"`
		} `json:"period"`
	} `json:"included"`
	Purchased *struct {
		Balance *ollamaUSD `json:"balance_usd"`
	} `json:"purchased"`
}

func (o OllamaAPI) Read(ctx context.Context, key string) (usage.Reading, error) {
	if isSession(key) {
		return usage.Reading{}, usage.ErrSkip
	}
	var bal ollamaBalance
	if err := getJSON(ctx, o.HTTP, base(o.BaseURL, "https://ollama.com")+"/api/balance", key, &bal); err != nil {
		return usage.Reading{}, err
	}
	return ollamaAPIReading(bal)
}

// ollamaAPIReading is the reading of an answer; an answer with neither an
// allowance nor a purchased balance is an error.
func ollamaAPIReading(bal ollamaBalance) (usage.Reading, error) {
	r := usage.Reading{Scope: "account", Subscription: bal.Included != nil}
	inc, pur := bal.Included, bal.Purchased
	if inc == nil && pur == nil {
		return usage.Reading{}, fmt.Errorf("accounts: Ollama balance in a shape this version cannot read")
	}
	if inc != nil {
		if inc.Allowance == nil || inc.Balance == nil || inc.Allowance.v < 0 {
			return usage.Reading{}, fmt.Errorf("accounts: Ollama balance in a shape this version cannot read")
		}
		if allowance := inc.Allowance.v; allowance > 0 {
			used := max(0, allowance-inc.Balance.v)
			w := usage.Window{Name: "month", UsedPct: clampPct(pct(used, allowance))}
			if inc.Period != nil {
				w.ResetsAt = inc.Period.Until.t
			}
			r.Windows = append(r.Windows, w)
		}
	}
	if pur != nil {
		if pur.Balance == nil {
			return usage.Reading{}, fmt.Errorf("accounts: Ollama balance in a shape this version cannot read")
		}
		r.BalanceUSD, r.HasBalance = pur.Balance.v, true
	}
	return r, nil
}

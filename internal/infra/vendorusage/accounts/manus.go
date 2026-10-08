package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerManus registers the reader (readers_gen.go).
func readerManus() usage.Reader { return Manus{} }

// Manus reads the credits manus.im shows its signed-in users, ported from
// CodexBar (Sources/CodexBarCore/Resources/Plugins/manus.js,
// Providers/Manus, docs/manus.md): the browser's session_id cookie is sent
// as a bearer token to POST
// api.manus.im/user.v1.UserService/GetAvailableCredits. The credential is
// the Cookie header holding session_id, or the bare session_id value
// (MANUS_SESSION_TOKEN).
//
// The Pro plan's monthly credits used are the "month" window (Manus gives
// no renewal date), the daily refresh credits used are the "day" window,
// and the total credits left are the balance in credits.
type Manus struct {
	BaseURL string
	HTTP    *http.Client
}

func (Manus) Endpoint() string               { return "manus" }
func (Manus) Provider() eventschema.Provider { return "manus" }
func (Manus) Source() string                 { return "manus-account" }

// manusEpoch is the 2001 reference date Manus's numeric refresh times
// count from (Foundation's reference date), in Unix seconds.
const manusEpoch = 978307200

func (m Manus) Read(ctx context.Context, key string) (usage.Reading, error) {
	token := sessionToken(key, "session_id")
	if token == "" {
		return usage.Reading{}, fmt.Errorf("%w (no session_id in the Manus session)", usage.ErrAuth)
	}
	header := http.Header{
		"Authorization":            {"Bearer " + token},
		"Origin":                   {"https://manus.im"},
		"Referer":                  {"https://manus.im/"},
		"Connect-Protocol-Version": {"1"},
		"User-Agent":               {browserUA},
	}
	var root map[string]json.RawMessage
	url := base(m.BaseURL, "https://api.manus.im") + "/user.v1.UserService/GetAvailableCredits"
	if err := postJSON(ctx, m.HTTP, url, header, map[string]any{}, &root); err != nil {
		return usage.Reading{}, err
	}
	credits, ok := manusCredits(root)
	if !ok {
		return usage.Reading{}, errors.New("accounts: manus credits: unrecognised answer")
	}
	r := usage.Reading{Scope: "account"}
	if credits.Total.ok {
		r.Credits, r.CreditsUnit, r.HasCredits = credits.Total.v, "credits", true
	}
	if monthly := credits.ProMonthly.v; monthly > 0 {
		r.Windows = append(r.Windows, usage.Window{Name: "month", UsedPct: clampPct(pct(monthly-credits.Periodic.v, monthly))})
	}
	if most := credits.MaxRefresh.v; most > 0 {
		r.Windows = append(r.Windows, usage.Window{Name: "day", Duration: 24 * time.Hour,
			UsedPct: clampPct(pct(most-credits.Refresh.v, most)), ResetsAt: manusTime(credits.NextRefresh)})
	}
	r.Subscription = len(r.Windows) > 0
	return r, nil
}

type manusCreditFields struct {
	Total       number          `json:"totalCredits"`
	Free        number          `json:"freeCredits"`
	Periodic    number          `json:"periodicCredits"`
	Addon       number          `json:"addonCredits"`
	Refresh     number          `json:"refreshCredits"`
	MaxRefresh  number          `json:"maxRefreshCredits"`
	ProMonthly  number          `json:"proMonthlyCredits"`
	Event       number          `json:"eventCredits"`
	NextRefresh json.RawMessage `json:"nextRefreshTime"`
}

// manusCredits finds the credits object: the answer itself or one of the
// envelopes Manus has used. It must name at least one credit field, so an
// error object is never read as zero credits.
func manusCredits(root map[string]json.RawMessage) (manusCreditFields, bool) {
	whole, _ := json.Marshal(root)
	candidates := []json.RawMessage{root["data"], root["result"], root["response"], root["availableCredits"], whole}
	for _, raw := range candidates {
		var fields map[string]json.RawMessage
		if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
			continue
		}
		known := false
		for _, k := range []string{"totalCredits", "freeCredits", "periodicCredits", "addonCredits",
			"refreshCredits", "maxRefreshCredits", "proMonthlyCredits", "eventCredits"} {
			if _, ok := fields[k]; ok {
				known = true
			}
		}
		if !known {
			continue
		}
		var out manusCreditFields
		if json.Unmarshal(raw, &out) == nil {
			return out, true
		}
	}
	return manusCreditFields{}, false
}

// manusTime reads a refresh time given as seconds since 2001 or as an ISO
// time.
func manusTime(raw json.RawMessage) time.Time {
	var n float64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		return time.Unix(int64(n)+manusEpoch, 0).UTC()
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && strings.Contains(s, "T") {
		return parseTime(s)
	}
	return time.Time{}
}

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

// readerLongCat registers the reader (readers_gen.go).
func readerLongCat() usage.Reader { return LongCat{} }

// LongCat reads the token quota the LongCat platform's usage page shows,
// ported from CodexBar (Sources/CodexBarCore/Resources/Plugins/longcat.ts,
// Providers/LongCat, docs/longcat.md), with the longcat.chat session's
// Cookie header (pasted at setup, or LONGCAT_MANUAL_COOKIE). LongCat's API
// keys read no usage.
//
// /api/v1/user-current proves the session. The active token pack
// (/api/pay/quota/metering/token-packs/summary) is the quota; without one,
// the legacy /api/lc-platform/v1/tokenUsage aggregate is. The share used is
// the "tokens" window (LongCat gives it no reset); the tokens left, with
// any pending fuel packs', are the balance in tokens.
type LongCat struct {
	BaseURL string
	HTTP    *http.Client
}

func (LongCat) Endpoint() string               { return "longcat" }
func (LongCat) Provider() eventschema.Provider { return "longcat" }
func (LongCat) Source() string                 { return "longcat-account" }

func (l LongCat) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !strings.Contains(cookie, "=") {
		return usage.Reading{}, fmt.Errorf("%w (the LongCat credential is not a Cookie header)", usage.ErrAuth)
	}
	host := base(l.BaseURL, "https://longcat.chat")
	header := http.Header{
		"Accept":          {"application/json, text/plain, */*"},
		"Accept-Language": {"en-US,en;q=0.9"},
		"Origin":          {"https://longcat.chat"},
		"Referer":         {"https://longcat.chat/platform/usage"},
		"User-Agent":      {browserUA},
		"Cookie":          {cookie},
	}
	get := func(path string, out any) error {
		body, err := getPage(ctx, l.HTTP, host+path, header)
		if err != nil {
			return err
		}
		return longCatUnwrap(body, host+path, out)
	}
	if err := get("/api/v1/user-current", &struct{}{}); err != nil {
		return usage.Reading{}, err
	}
	var total, used number
	var summary struct {
		Lot *struct {
			Status   string `json:"status"`
			Total    number `json:"totalToken"`
			Consumed number `json:"consumedToken"`
		} `json:"currentLot"`
	}
	var envelope json.RawMessage
	// The summary is best effort: some sessions' cookies are scoped to
	// other paths.
	if postJSON(ctx, l.HTTP, host+"/api/pay/quota/metering/token-packs/summary", header, map[string]any{}, &envelope) == nil &&
		longCatUnwrap(envelope, host, &summary) == nil &&
		summary.Lot != nil && strings.EqualFold(summary.Lot.Status, "ACTIVE") && summary.Lot.Total.v > 0 {
		total, used = summary.Lot.Total, summary.Lot.Consumed
		used.ok = true
	} else {
		var legacy struct {
			Usage *struct {
				Total     number `json:"totalToken"`
				Used      number `json:"usedToken"`
				Available number `json:"availableToken"`
			} `json:"usage"`
			Total     number `json:"totalToken"`
			Used      number `json:"usedToken"`
			Available number `json:"availableToken"`
		}
		if err := get("/api/lc-platform/v1/tokenUsage", &legacy); err != nil {
			return usage.Reading{}, err
		}
		total, used = legacy.Total, legacy.Used
		available := legacy.Available
		if legacy.Usage != nil {
			total, used, available = legacy.Usage.Total, legacy.Usage.Used, legacy.Usage.Available
		}
		if !total.ok {
			return usage.Reading{}, errors.New("accounts: longcat tokenUsage: unrecognised answer")
		}
		if !used.ok {
			used = number{v: total.v - available.v, ok: true}
			if !available.ok {
				used.v = 0
			}
		}
	}
	r := usage.Reading{Scope: "account"}
	left := 0.0
	if total.v > 0 {
		u := max(0, used.v)
		r.Subscription = true
		r.Windows = []usage.Window{{Name: "tokens", UsedPct: clampPct(pct(u, total.v))}}
		left = max(0, total.v-u)
		r.Credits, r.CreditsUnit, r.HasCredits = left, "tokens", true
	}
	var fuel struct {
		Total number `json:"totalQuota"`
		List  []struct {
			Available number `json:"availableToken"`
		} `json:"list"`
	}
	if get("/api/lc-platform/v1/pending-fuel-packages", &fuel) == nil {
		remaining, seen := 0.0, false
		for _, p := range fuel.List {
			if p.Available.ok {
				remaining, seen = remaining+p.Available.v, true
			}
		}
		if !seen && fuel.Total.ok {
			remaining = fuel.Total.v
		}
		if remaining > 0 {
			r.Credits, r.CreditsUnit, r.HasCredits = left+remaining, "tokens", true
		}
	}
	return r, nil
}

// longCatUnwrap decodes LongCat's {code, message, data} envelope: a code of
// 401 or 403 is a refused session, any other but 0 or 200 an error; data
// (or the answer itself, unwrapped) is decoded into out.
func longCatUnwrap(body []byte, where string, out any) error {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("accounts: longcat %s: %w", hostPath(where), err)
	}
	if raw, ok := env["code"]; ok {
		var code number
		_ = json.Unmarshal(raw, &code)
		switch {
		case !code.ok:
			return fmt.Errorf("accounts: longcat %s: unreadable code", hostPath(where))
		case code.v == 401 || code.v == 403:
			return fmt.Errorf("%w (code %v on %s)", usage.ErrAuth, code.v, hostPath(where))
		case code.v != 0 && code.v != 200:
			return fmt.Errorf("accounts: longcat %s: code %v", hostPath(where), code.v)
		}
	}
	data := json.RawMessage(body)
	if raw, ok := env["data"]; ok {
		data = raw
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("accounts: longcat %s: %w", hostPath(where), err)
	}
	return nil
}

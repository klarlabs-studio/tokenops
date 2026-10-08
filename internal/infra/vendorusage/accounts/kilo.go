package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerKilo registers the reader (readers_gen.go).
func readerKilo() usage.Reader { return Kilo{} }

// Kilo reads two tRPC procedures in one batched GET on app.kilo.ai, as
// CodexBar's Kilo provider does: user.getCreditBlocks (prepaid credit,
// in micro-dollars) and kiloPass.getState (the Kilo Pass subscription's
// credits this billing period, in dollars). It reads the personal account,
// or, with a scope set (vendor_usage.accounts.scopes.kilo), the
// organisation of that ID, named in the X-KILOCODE-ORGANIZATIONID header
// as CodexBar does (Providers/Kilo/KiloUsageFetcher.swift).
type Kilo struct {
	BaseURL string
	HTTP    *http.Client
	// Organization is the organisation ID read; empty reads the personal
	// account.
	Organization string
}

// WithScope reads the organisation scope names.
func (k Kilo) WithScope(scope string) usage.Reader {
	k.Organization = strings.TrimSpace(scope)
	return k
}

func (Kilo) Endpoint() string               { return "kilo" }
func (Kilo) Provider() eventschema.Provider { return "kilo" }
func (Kilo) Source() string                 { return "kilo-account" }

const kiloProcedures = "user.getCreditBlocks,kiloPass.getState"

// kiloEntry is one procedure's answer in a tRPC batch.
type kiloEntry struct {
	Result *struct {
		Data struct {
			JSON json.RawMessage `json:"json"`
		} `json:"data"`
	} `json:"result"`
	Error *struct {
		JSON struct {
			Message string `json:"message"`
			Data    struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"json"`
	} `json:"error"`
}

func (k Kilo) Read(ctx context.Context, key string) (usage.Reading, error) {
	input := url.QueryEscape(`{"0":{"json":null},"1":{"json":null}}`)
	endpoint := base(k.BaseURL, "https://app.kilo.ai") + "/api/trpc/" + kiloProcedures + "?batch=1&input=" + input
	header := http.Header{"Authorization": {"Bearer " + key}}
	scope := "account"
	if k.Organization != "" {
		header.Set("X-KILOCODE-ORGANIZATIONID", k.Organization)
		scope = "organisation"
	}
	var entries []kiloEntry
	if err := doJSON(ctx, k.HTTP, http.MethodGet, endpoint, header, nil, &entries); err != nil {
		return usage.Reading{}, err
	}
	if len(entries) < 2 {
		return usage.Reading{}, errors.New("accounts: GET app.kilo.ai/api/trpc: unexpected batch shape")
	}
	for _, e := range entries[:2] {
		if err := kiloError(e); err != nil {
			return usage.Reading{}, err
		}
	}
	r := usage.Reading{Scope: scope}
	if balance, ok := kiloBalance(entries[0]); ok {
		r.BalanceUSD, r.HasBalance = balance, true
	}
	if w, ok := kiloPass(entries[1]); ok {
		r.Subscription = true
		r.Windows = []usage.Window{w}
	}
	return r, nil
}

func kiloError(e kiloEntry) error {
	if e.Error == nil {
		return nil
	}
	code := strings.ToUpper(e.Error.JSON.Data.Code)
	if code == "UNAUTHORIZED" || code == "FORBIDDEN" {
		return fmt.Errorf("%w (tRPC %s on app.kilo.ai/api/trpc)", usage.ErrAuth, code)
	}
	return fmt.Errorf("accounts: GET app.kilo.ai/api/trpc: tRPC error %s", code)
}

// kiloBalance sums the credit blocks' balances; an account with no
// blocks reports its total balance instead.
func kiloBalance(e kiloEntry) (float64, bool) {
	if e.Result == nil {
		return 0, false
	}
	var p struct {
		CreditBlocks []struct {
			BalanceMicroUSD number `json:"balance_mUsd"`
		} `json:"creditBlocks"`
		TotalBalanceMicroUSD number `json:"totalBalance_mUsd"`
	}
	if json.Unmarshal(e.Result.Data.JSON, &p) != nil {
		return 0, false
	}
	sum, seen := 0.0, false
	for _, b := range p.CreditBlocks {
		if b.BalanceMicroUSD.ok {
			sum, seen = sum+b.BalanceMicroUSD.v, true
		}
	}
	if !seen {
		if !p.TotalBalanceMicroUSD.ok {
			return 0, false
		}
		sum = p.TotalBalanceMicroUSD.v
	}
	return max(0, sum/1e6), true
}

// kiloPass is the Kilo Pass credits used this billing period against the
// period's base and bonus credits; null when there is no subscription.
func kiloPass(e kiloEntry) (usage.Window, bool) {
	if e.Result == nil {
		return usage.Window{}, false
	}
	var p struct {
		Subscription *struct {
			UsageUSD       number `json:"currentPeriodUsageUsd"`
			BaseCreditsUSD number `json:"currentPeriodBaseCreditsUsd"`
			BonusUSD       number `json:"currentPeriodBonusCreditsUsd"`
			NextBillingAt  string `json:"nextBillingAt"`
			NextRenewalAt  string `json:"nextRenewalAt"`
		} `json:"subscription"`
	}
	if json.Unmarshal(e.Result.Data.JSON, &p) != nil || p.Subscription == nil {
		return usage.Window{}, false
	}
	s := p.Subscription
	if !s.BaseCreditsUSD.ok {
		return usage.Window{}, false
	}
	total := max(0, s.BaseCreditsUSD.v) + max(0, s.BonusUSD.v)
	used := 100.0 // a pass with no credits is spent
	if total > 0 {
		used = clampPct(pct(max(0, s.UsageUSD.v), total))
	}
	reset := parseTime(s.NextBillingAt)
	if reset.IsZero() {
		reset = parseTime(s.NextRenewalAt)
	}
	return usage.Window{Name: "month", UsedPct: used, ResetsAt: reset}, true
}

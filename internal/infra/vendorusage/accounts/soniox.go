package accounts

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerSoniox registers the reader (readers_gen.go).
func readerSoniox() usage.Reader { return Soniox{} }

// Soniox reads the organisation's prepaid balance and this month's spend
// against its budget from Soniox's Organization API
// (soniox.com/docs/guides/organization-api): GET
// /v1/organization/balance and /v1/organization/usage/monthly, ported from
// CodexBar (Sources/CodexBarCore/Resources/Plugins/soniox.ts,
// Providers/Soniox, docs/soniox.md). It needs an organisation API key with
// Balance and spend and Usage and limits access; a project key is
// refused. An account billed by invoice has no prepaid balance to report.
type Soniox struct {
	BaseURL string
	HTTP    *http.Client
}

func (Soniox) Endpoint() string               { return "soniox" }
func (Soniox) Provider() eventschema.Provider { return "soniox" }
func (Soniox) Source() string                 { return "soniox-organization" }

var (
	sonioxMoney = lazyRegexp(`^-?\d+(?:\.\d+)?$`)
	sonioxMonth = lazyRegexp(`^\d{4}-(0[1-9]|1[0-2])$`)
)

var errSonioxShape = errors.New("accounts: soniox billing: unrecognised answer")

func (s Soniox) Read(ctx context.Context, key string) (usage.Reading, error) {
	root := base(s.BaseURL, "https://api.soniox.com") + "/v1/organization/"
	var balance struct {
		Available *string `json:"available_balance_usd"`
		Invoice   *bool   `json:"invoice_billing_enabled"`
	}
	if err := getJSON(ctx, s.HTTP, root+"balance", key, &balance); err != nil {
		return usage.Reading{}, err
	}
	var monthly struct {
		Usage  *string `json:"usage_usd"`
		Budget *string `json:"monthly_budget_usd"`
		Month  string  `json:"month"`
	}
	if err := getJSON(ctx, s.HTTP, root+"usage/monthly", key, &monthly); err != nil {
		return usage.Reading{}, err
	}
	available, ok1 := sonioxAmount(balance.Available, true)
	used, ok2 := sonioxAmount(monthly.Usage, false)
	budget, ok3 := sonioxAmount(monthly.Budget, false)
	if !ok1 || !ok2 || !ok3 || balance.Invoice == nil || !sonioxMonth().MatchString(monthly.Month) {
		return usage.Reading{}, errSonioxShape
	}
	r := usage.Reading{Scope: "organization", UsedUSD: used, HasUsed: true, LimitUSD: budget}
	if !*balance.Invoice {
		r.BalanceUSD, r.HasBalance = available, true
	}
	if budget > 0 {
		start, _ := time.Parse("2006-01", monthly.Month)
		r.Windows = []usage.Window{{Name: "month", UsedPct: clampPct(pct(used, budget)),
			Duration: start.AddDate(0, 1, 0).Sub(start), ResetsAt: start.AddDate(0, 1, 0)}}
	}
	return r, nil
}

// sonioxAmount reads a decimal string of dollars; signed allows a
// negative one.
func sonioxAmount(s *string, signed bool) (float64, bool) {
	if s == nil || !sonioxMoney().MatchString(strings.TrimSpace(*s)) {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(*s), 64)
	if err != nil || (!signed && v < 0) {
		return 0, false
	}
	return v, true
}

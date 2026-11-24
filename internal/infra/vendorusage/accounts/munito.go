package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerMunito registers the reader (readers_gen.go).
func readerMunito() usage.Reader { return Munito{} }

// Munito reads the organisation's spend against its limits from Munito's
// Admin API, GET api.munito.ai/admin/v1/limits (docs.munito.ai/api/
// getSpendLimits), ported from CodexBar (Sources/CodexBarCore/Resources/
// Plugins/munito.ts, Providers/Munito, docs/munito.md). It needs an admin
// key with "Read account data"; an inference key is refused.
//
// The month's spend is the spend against its limit; without a month
// period, the day's spend over the last day. A day with a limit is the
// "day" window, with no reset: Munito reports none. A period whose
// stopped_until is still ahead is spending halted at its limit.
type Munito struct {
	BaseURL string
	HTTP    *http.Client
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

func (Munito) Endpoint() string               { return "munito" }
func (Munito) Provider() eventschema.Provider { return "munito" }
func (Munito) Source() string                 { return "munito-admin" }

var errMunitoShape = errors.New("accounts: munito spend limits: unrecognised answer")

type munitoPeriod struct {
	spent, limit float64
	hasLimit     bool
	stopped      time.Time
}

func (m Munito) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Periods *[]struct {
			Period       string          `json:"period"`
			SpentUSD     json.RawMessage `json:"spent_usd"`
			LimitUSD     json.RawMessage `json:"limit_usd"`
			StoppedUntil *string         `json:"stopped_until"`
		} `json:"periods"`
	}
	if err := getJSON(ctx, m.HTTP, base(m.BaseURL, "https://api.munito.ai")+"/admin/v1/limits", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Periods == nil {
		return usage.Reading{}, errMunitoShape
	}
	periods := map[string]munitoPeriod{}
	for _, p := range *resp.Periods {
		kind := strings.TrimSpace(p.Period)
		if kind != "day" && kind != "month" {
			continue
		}
		spent, ok := munitoMoney(p.SpentUSD)
		if !ok {
			return usage.Reading{}, errMunitoShape
		}
		mp := munitoPeriod{spent: spent}
		if len(p.LimitUSD) > 0 && string(p.LimitUSD) != "null" {
			if mp.limit, ok = munitoMoney(p.LimitUSD); !ok {
				return usage.Reading{}, errMunitoShape
			}
			mp.hasLimit = mp.limit > 0
		}
		if p.StoppedUntil != nil && strings.TrimSpace(*p.StoppedUntil) != "" {
			t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*p.StoppedUntil))
			if err != nil {
				return usage.Reading{}, errMunitoShape
			}
			mp.stopped = t
		}
		periods[kind] = mp
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	r := usage.Reading{Scope: "organization"}
	day, hasDay := periods["day"]
	month, hasMonth := periods["month"]
	switch {
	case hasMonth:
		r.UsedUSD, r.HasUsed = month.spent, true
		if month.hasLimit {
			r.LimitUSD = month.limit
		}
	case hasDay:
		r.UsedUSD, r.HasUsed, r.UsedPeriod = day.spent, true, 24*time.Hour
		if day.hasLimit {
			r.LimitUSD = day.limit
		}
	}
	if hasDay && day.hasLimit {
		r.Windows = []usage.Window{{Name: "day", UsedPct: clampPct(pct(day.spent, day.limit)), Duration: 24 * time.Hour}}
	}
	for _, p := range periods {
		if p.stopped.After(now()) {
			r.LimitReached = true
		}
	}
	return r, nil
}

// munitoMoney reads a non-negative amount given as a number or a numeric
// string.
func munitoMoney(raw json.RawMessage) (float64, bool) {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(string(raw)), `"`))
	v, err := strconv.ParseFloat(s, 64)
	if s == "" || err != nil || v < 0 || strings.ContainsAny(s, "eEnN+") {
		return 0, false
	}
	return v, true
}

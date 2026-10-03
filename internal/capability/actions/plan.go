package actions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
)

// PlanRequest binds a provider to a plan, or clears its binding.
type PlanRequest struct {
	Provider      string
	Plan          string
	SpendLimitUSD float64
	LimitWindow   string
	RateFactor    float64
	Clear         bool
	// Price is what the operator pays per month, on their bill, in
	// Currency (default: the configured money currency, else USD).
	Price    float64
	Currency string
	// Since is the date the plan took effect, when before today. Usage
	// recorded as billed since then is re-marked as plan-covered.
	Since string
	// Actor names who made the change in the audit log: mcp, api, cli.
	Actor string
}

// PlanChange is the binding after a change.
type PlanChange struct {
	Provider    string `json:"provider"`
	Config      string `json:"config"`
	Plan        string `json:"plan,omitempty"`
	Previous    string `json:"previous,omitempty"`
	RenamedFrom string `json:"renamed_from,omitempty"`
	Cleared     bool   `json:"cleared,omitempty"`
	// Since, Restamped and RestampedTo report a backdated binding.
	Since       string `json:"since,omitempty"`
	Restamped   int64  `json:"restamped,omitempty"`
	RestampedTo string `json:"restamped_to,omitempty"`
	// HistoryError says the switch was bound but not recorded in full.
	HistoryError string `json:"history_error,omitempty"`
}

// SetPlan binds req.Provider to req.Plan (or clears it), writes config,
// and records the switch in the plan history and the audit log. A
// backdated binding re-marks the provider's usage since then.
func SetPlan(ctx context.Context, path string, req PlanRequest, now time.Time) (PlanChange, error) {
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		return PlanChange{}, inputErr(fmt.Errorf("provider is required"))
	}
	from, err := planStart(req.Since, now)
	if err != nil {
		return PlanChange{}, inputErr(err)
	}
	out := PlanChange{Provider: provider, Config: path}
	var previous, next string
	cfg, err := edit(path, func(c *config.Config) error {
		previous = c.Plans[provider]
		if req.Clear {
			delete(c.Plans, provider)
			out.Cleared = true
			return nil
		}
		b, err := c.BindPlan(provider, strings.TrimSpace(req.Plan), config.PlanLimit{
			SpendLimitUSD: req.SpendLimitUSD, Window: req.LimitWindow, RateFactor: req.RateFactor,
		})
		if err != nil {
			return inputErr(err)
		}
		out.Plan, next = b.Plan, b.Plan
		if b.Previous != "" && b.Previous != b.Plan {
			out.Previous = b.Previous
		}
		out.RenamedFrom = b.RenamedFrom
		return nil
	})
	if err != nil {
		return PlanChange{}, err
	}
	if req.Clear {
		from = now // an unset is never backdated
	}
	if previous == next && req.Since == "" && req.Price <= 0 && req.SpendLimitUSD <= 0 {
		return out, nil
	}
	price, currency := req.Price, ""
	if req.Clear {
		price = 0
	} else if price > 0 {
		currency = firstNonEmpty(req.Currency, cfg.Money.Currency, "USD")
	}
	res, err := planhistory.Record(ctx, planhistory.Switch{
		Provider: provider, Previous: previous, Plan: next, From: from, Now: now,
		DBPath: StorePath(cfg.Storage.Path), Actor: firstNonEmpty(req.Actor, "tokenops"),
		Price: price, Currency: currency, SpendLimitUSD: req.SpendLimitUSD,
	})
	switch {
	case err != nil:
		out.HistoryError = err.Error()
	case res.StoreErr != nil:
		out.HistoryError = "event store unavailable, so earlier usage was not re-marked: " + res.StoreErr.Error()
	case from.Before(now) && next != "":
		out.Since = from.Format("2006-01-02")
		out.Restamped = res.Restamped
		out.RestampedTo = res.RestampedTo
	}
	return out, nil
}

// planStart is when a binding takes effect: now, or the date given.
func planStart(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return now, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("since %q: want a date (2026-09-01) or an RFC3339 time", s)
	}
	return t.UTC(), nil
}

// StorePath is the event store the daemon writes, from the configured
// path as written in the file.
func StorePath(configured string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(configured)
	switch {
	case p == "":
		return filepath.Join(home, ".tokenops", "events.db")
	case p == "~" || strings.HasPrefix(p, "~/"):
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

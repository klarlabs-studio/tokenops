package spending

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/planswitch"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/focus"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// ChargeReader reads a window's usage per day, provider and model, split
// into plan-covered and metered. *EventAggregator implements it.
type ChargeReader interface {
	DailyCharges(ctx context.Context, w Window) ([]analytics.Charge, error)
}

// FocusQuery selects what a FOCUS export covers.
type FocusQuery struct {
	// Since is floored to its UTC day; zero is the start of Now's month.
	// Until zero is Now.
	Since, Until   time.Time
	IncludeSources []string
	// History is the recorded plan changes and Plans the plans bound now:
	// together they say which plan was in force when (ADR 0008).
	History plans.History
	Plans   map[string]string
	Now     time.Time
	// Allocation, when it holds rules, splits each charge by the
	// allocation tags its turns carry and fills the Tags column. Its
	// Turns may be nil: they are read from the charge reader.
	Allocation *allocation.Deps
}

// FocusDataset is spend and usage as FOCUS rows.
type FocusDataset struct {
	// Version is the FOCUS version the rows conform to.
	Version      string
	Since, Until time.Time
	Rows         []focus.Row
	// Notes say what the dataset understates or leaves out, one line each.
	Notes []string
}

// WriteCSV writes the dataset as CSV.
func (d FocusDataset) WriteCSV(w io.Writer) error { return focus.WriteCSV(w, d.Rows) }

// ErrEmptyWindow refuses a window that ends before it starts.
var ErrEmptyWindow = errors.New("the window ends before it starts")

// FocusExport renders the window's spend and usage as a FOCUS dataset:
// plan fees as purchases, plan-covered usage billed 0 at its API list
// price, metered usage at what it cost. Only derived figures go in it.
func FocusExport(ctx context.Context, src ChargeReader, q FocusQuery) (FocusDataset, error) {
	now := q.Now.UTC()
	since, until := q.Since.UTC(), q.Until.UTC()
	if q.Since.IsZero() {
		since = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	since = since.Truncate(24 * time.Hour)
	if q.Until.IsZero() {
		until = now
	}
	if !until.After(since) {
		return FocusDataset{}, ErrEmptyWindow
	}
	charges, err := src.DailyCharges(ctx, Window{Since: since, Until: until, IncludeSources: q.IncludeSources})
	if err != nil {
		return FocusDataset{}, err
	}
	turns, err := allocate(ctx, src, q, since, until)
	if err != nil {
		return FocusDataset{}, err
	}
	d := FocusDataset{Version: focus.Version, Since: since, Until: until}
	usage, unpriced := focusUsage(splitByTags(charges, turns), q.History, q.Plans)
	purchases, notes := focusPurchases(q.History, q.Plans, since, until)
	d.Rows = focus.Build(usage, purchases)
	if unpriced > 0 {
		d.Notes = append(d.Notes, fmt.Sprintf(
			"%d charge(s) include models with no API rate, so their ListCost understates them", unpriced))
	}
	d.Notes = append(d.Notes, notes...)
	return d, nil
}

// focusUsage maps charges to FOCUS usage, naming the plan in force on
// each plan-covered charge's day.
func focusUsage(charges []taggedCharge, h plans.History, bound map[string]string) ([]focus.Usage, int) {
	out := make([]focus.Usage, 0, len(charges))
	unpriced := 0
	for _, c := range charges {
		u := focus.Usage{
			Day: c.Day, Provider: c.Provider, Model: c.Model, PlanCovered: c.PlanCovered,
			Requests: c.Requests, Tokens: c.Tokens(), BilledUSD: c.CostUSD, ListUSD: c.ListUSD, Unpriced: c.Unpriced,
			Tags: c.Tags,
		}
		if c.PlanCovered {
			if periods := h.Periods(c.Provider, c.Day, c.Day.Add(24*time.Hour), bound[c.Provider]); len(periods) > 0 {
				u.Plan = planOf(periods[len(periods)-1].Plan)
			}
		}
		if c.Unpriced {
			unpriced++
		}
		out = append(out, u)
	}
	return out, unpriced
}

// focusPurchases are the fees of the plans in force over [since, until),
// each in the currency it is priced in.
func focusPurchases(h plans.History, bound map[string]string, since, until time.Time) ([]focus.Purchase, []string) {
	providers := map[string]bool{}
	for p, plan := range bound {
		if plan != "" {
			providers[p] = true
		}
	}
	for _, b := range h {
		providers[b.Provider] = true
	}
	names := make([]string, 0, len(providers))
	for p := range providers {
		names = append(names, p)
	}
	sort.Strings(names)
	var (
		out   []focus.Purchase
		notes []string
	)
	for _, provider := range names {
		for _, period := range h.Periods(provider, since, until, bound[provider]) {
			price, fee := planswitch.PriceOf(period)
			if !fee {
				continue
			}
			if price.Monthly <= 0 {
				notes = append(notes, fmt.Sprintf("%s has no price, so its fee is left out; give it one with `tokenops plan set %s %s --price <monthly>`",
					period.Plan, provider, period.Plan))
				continue
			}
			out = append(out, focus.Purchase{
				Provider: provider, Plan: planOf(period.Plan), From: period.From, To: period.To,
				Amount: plans.PeriodCost(price.Monthly, period), Currency: price.Currency,
				Months: period.Months(), ListPriced: price.Source == "list",
			})
		}
	}
	return out, notes
}

func planOf(id string) focus.Plan {
	p := focus.Plan{ID: id}
	if c, ok := plans.Lookup(id); ok {
		p.Display = c.Display
	}
	return p
}

// Package anomaly spots a day whose spend or usage falls outside what
// the operator's own history forecasts, and ranks what drove it.
//
// A day is unusual when its actual is above the upper bound of the
// forecast's 95% interval (spend/forecast, from the days before it) by a
// minimum effect: a dollar amount and a share of the forecast. Nothing is
// flagged before MinHistoryDays of history, when the forecast is mostly
// noise. The rise is then decomposed by model, biller, billing basis,
// repository, session and tool, and the contributors are ranked by how
// many dollars each added against its own daily mean. A few causes are
// recognised by name: a pricier model taking over, one session carrying
// the rise (a likely runaway loop), and usage billed per token where a
// plan should cover it.
//
// It is pure and holds derived figures only: dollars, tokens, shares and
// the names of models, billers, repositories, sessions and tools.
package anomaly

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast"
)

// The metrics a day is measured in.
const (
	// MetricSpend is what was billed: per-token charges, not plan fees.
	MetricSpend = "spend"
	// MetricUsage is the usage at API list prices, plan-covered included:
	// its value where a plan covers it.
	MetricUsage = "usage"
)

// The dimensions a rise is decomposed by.
const (
	DimModel   = "model"
	DimBiller  = "biller"
	DimBilling = "billing"
	DimRepo    = "repo"
	DimSession = "session"
	DimTool    = "tool"
)

// dims is the decomposition order, coarse to fine; it breaks ties.
var dims = []string{DimModel, DimBiller, DimBilling, DimRepo, DimTool, DimSession}

// The causes recognised by name.
const (
	// CausePricierModel is a model that was rare before taking most of
	// the day's tokens at a higher price per token.
	CausePricierModel = "pricier_model"
	// CauseMeteredOnPlan is usage billed per token for a provider whose
	// plan should cover it: a key or a gateway route bypassing the plan.
	CauseMeteredOnPlan = "metered_on_plan"
	// CauseRunawayLoop is one session carrying most of the rise.
	CauseRunawayLoop = "runaway_loop"
	// CauseSpike is a rise none of the above explains; its contributors
	// say where it came from.
	CauseSpike = "spike"
)

// Values a dimension takes when the item does not name one.
const (
	NoSession    = "outside a session"
	NoRepository = "no repository"
	Unknown      = "unknown"
	BillingPlan  = "plan"
	BillingToken = "per token"
)

// Item is some usage on one UTC day, as fine as the dimensions split it.
type Item struct {
	// Day is the UTC midnight the usage happened on.
	Day time.Time
	// Provider is who bills it (ADR 0009); Endpoint the route it went
	// through, when one is recorded.
	Provider, Endpoint string
	Model, Repo        string
	Session, Tool      string
	// PlanCovered is usage a plan covered, billed nothing at the margin.
	PlanCovered bool
	Tokens      int64
	// CostUSD is what was billed; ValueUSD the usage at list prices.
	CostUSD, ValueUSD float64
}

func (i Item) amount(metric string) float64 {
	if metric == MetricSpend {
		return i.CostUSD
	}
	return i.ValueUSD
}

// Biller names who billed the item, and through which route when the
// route is not the provider's own.
func (i Item) Biller() string {
	if i.Endpoint == "" || i.Endpoint == i.Provider {
		return orUnknown(i.Provider)
	}
	return orUnknown(i.Provider) + " via " + i.Endpoint
}

func (i Item) value(dim string) string {
	switch dim {
	case DimModel:
		return orUnknown(i.Model)
	case DimBiller:
		return i.Biller()
	case DimBilling:
		if i.PlanCovered {
			return BillingPlan
		}
		return BillingToken
	case DimRepo:
		if i.Repo == "" {
			return NoRepository
		}
		return i.Repo
	case DimSession:
		if i.Session == "" {
			return NoSession
		}
		return i.Session
	case DimTool:
		return orUnknown(i.Tool)
	}
	return Unknown
}

func orUnknown(s string) string {
	if s == "" {
		return Unknown
	}
	return s
}

// Config tunes detection; zero fields take the defaults.
type Config struct {
	// MinHistoryDays is how many days of history a day needs before it
	// can be flagged. Default 10.
	MinHistoryDays int
	// HistoryDays is how many days before a day its forecast reads.
	// Default 28.
	HistoryDays int
	// MinExcessUSD and MinExcessShare are the effect-size floor: the
	// actual must exceed the forecast by both. Defaults $5 and 25%.
	MinExcessUSD   float64
	MinExcessShare float64
	// PlanProviders are the providers a plan covers: usage they bill per
	// token is CauseMeteredOnPlan.
	PlanProviders map[string]bool
}

// The defaults.
const (
	DefaultMinHistoryDays = 10
	DefaultHistoryDays    = 28
	DefaultMinExcessUSD   = 5.0
	DefaultMinExcessShare = 0.25
)

func (c Config) withDefaults() Config {
	if c.MinHistoryDays <= 0 {
		c.MinHistoryDays = DefaultMinHistoryDays
	}
	if c.HistoryDays < c.MinHistoryDays {
		c.HistoryDays = max(DefaultHistoryDays, c.MinHistoryDays)
	}
	if c.MinExcessUSD <= 0 {
		c.MinExcessUSD = DefaultMinExcessUSD
	}
	if c.MinExcessShare <= 0 {
		c.MinExcessShare = DefaultMinExcessShare
	}
	return c
}

// Contributor is one value of one dimension and what it added.
type Contributor struct {
	Dimension string `json:"dimension"`
	Value     string `json:"value"`
	// ActualUSD is its amount on the day; BaselineUSD its mean per day
	// over the history; DeltaUSD the difference.
	ActualUSD   float64 `json:"actual_usd"`
	BaselineUSD float64 `json:"baseline_usd"`
	DeltaUSD    float64 `json:"delta_usd"`
	// Share is DeltaUSD over the day's whole rise against its mean. The
	// shares of one dimension can pass 1 when other values fell.
	Share float64 `json:"share"`
}

// Cause is the likely cause, named.
type Cause struct {
	Kind      string `json:"kind"`
	Dimension string `json:"dimension,omitempty"`
	Value     string `json:"value,omitempty"`
	// Pattern is what an "expected" answer stops flagging: the cause and
	// what it rests on, without the day.
	Pattern  string  `json:"pattern"`
	DeltaUSD float64 `json:"delta_usd,omitempty"`
	Share    float64 `json:"share,omitempty"`
	// Repo is where a runaway session ran.
	Repo string `json:"repo,omitempty"`
	// USDPerMTok and BaselineUSDPerMTok compare a pricier model's price
	// per million tokens with the history's.
	USDPerMTok         float64 `json:"usd_per_mtok,omitempty"`
	BaselineUSDPerMTok float64 `json:"baseline_usd_per_mtok,omitempty"`
	// Endpoint is the route metered usage under a plan went through;
	// CoveragePct and BaselineCoveragePct the share of the provider's
	// tokens its plan covered on the day and over the history.
	Endpoint            string   `json:"endpoint,omitempty"`
	CoveragePct         *float64 `json:"coverage_pct,omitempty"`
	BaselineCoveragePct *float64 `json:"baseline_coverage_pct,omitempty"`
}

// Anomaly is one unusual day.
type Anomaly struct {
	// ID names the anomaly in the follow-through ledger, so it can be
	// answered like a tip: anomaly-<metric>-<day>.
	ID     string    `json:"id"`
	Metric string    `json:"metric"`
	Day    time.Time `json:"day"`
	// Partial is today, still running: its actual can only grow.
	Partial     bool `json:"partial,omitempty"`
	HistoryDays int  `json:"history_days"`
	// ActualUSD is the day's amount; ExpectedUSD and UpperUSD the
	// forecast and its 95% upper bound; ExcessUSD actual minus forecast.
	ActualUSD   float64 `json:"actual_usd"`
	ExpectedUSD float64 `json:"expected_usd"`
	UpperUSD    float64 `json:"upper_usd"`
	ExcessUSD   float64 `json:"excess_usd"`
	// Contributors are ranked by DeltaUSD, at most perDimension of each
	// dimension.
	Contributors []Contributor `json:"contributors"`
	Cause        Cause         `json:"cause"`
}

// perDimension bounds the contributors kept per dimension.
const perDimension = 3

// Detect checks yesterday and today so far, in UTC days, and returns the
// days that are unusual: at most one per day, spend before usage, since
// money billed is the sharper signal.
func Detect(items []Item, now time.Time, cfg Config) []Anomaly {
	cfg = cfg.withDefaults()
	if len(items) == 0 {
		return nil
	}
	first := items[0].Day
	byDay := map[time.Time][]Item{}
	for _, it := range items {
		d := midnight(it.Day)
		byDay[d] = append(byDay[d], it)
		if d.Before(first) {
			first = d
		}
	}
	today := midnight(now)
	var out []Anomaly
	for _, d := range []time.Time{today.AddDate(0, 0, -1), today} {
		for _, metric := range []string{MetricSpend, MetricUsage} {
			if a, ok := check(byDay, first, d, metric, cfg); ok {
				a.Partial = d.Equal(today)
				out = append(out, a)
				break
			}
		}
	}
	return out
}

func midnight(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// check forecasts day d from the days before it and, when d is above the
// band by the effect floor, decomposes it.
func check(byDay map[time.Time][]Item, first, d time.Time, metric string, cfg Config) (Anomaly, bool) {
	start := d.AddDate(0, 0, -cfg.HistoryDays)
	if first.After(start) {
		start = first
	}
	n := int(d.Sub(start).Hours() / 24)
	if n < cfg.MinHistoryDays {
		return Anomaly{}, false
	}
	history := make([]forecast.Point, 0, n)
	var past []Item
	for day := start; day.Before(d); day = day.AddDate(0, 0, 1) {
		history = append(history, forecast.Point{At: day, Value: total(byDay[day], metric)})
		past = append(past, byDay[day]...)
	}
	preds := forecast.AutoForecast(history, 1, 24*time.Hour)
	if len(preds) == 0 {
		return Anomaly{}, false
	}
	p := preds[0]
	actual := total(byDay[d], metric)
	excess := actual - p.Value
	if actual <= p.Upper || excess < cfg.MinExcessUSD || (p.Value > 0 && excess/p.Value < cfg.MinExcessShare) {
		return Anomaly{}, false
	}
	a := Anomaly{
		ID: fmt.Sprintf("anomaly-%s-%s", metric, d.Format(time.DateOnly)), Metric: metric, Day: d, HistoryDays: n,
		ActualUSD: actual, ExpectedUSD: p.Value, UpperUSD: p.Upper, ExcessUSD: excess,
	}
	rise := actual - total(past, metric)/float64(n)
	all := decompose(byDay[d], past, n, metric, rise)
	a.Cause = name(all, byDay[d], past, n, rise, metric, cfg)
	a.Contributors = rank(all)
	return a, true
}

func total(items []Item, metric string) float64 {
	var s float64
	for _, it := range items {
		s += it.amount(metric)
	}
	return s
}

// decompose sets each value of each dimension against its own daily mean.
func decompose(today, past []Item, days int, metric string, rise float64) map[string][]Contributor {
	out := map[string][]Contributor{}
	for _, dim := range dims {
		actual, base := map[string]float64{}, map[string]float64{}
		for _, it := range today {
			actual[it.value(dim)] += it.amount(metric)
		}
		for _, it := range past {
			base[it.value(dim)] += it.amount(metric) / float64(days)
		}
		var cs []Contributor
		for v, x := range actual {
			c := Contributor{Dimension: dim, Value: v, ActualUSD: x, BaselineUSD: base[v], DeltaUSD: x - base[v]}
			if c.DeltaUSD <= 0 {
				continue
			}
			if rise > 0 {
				c.Share = c.DeltaUSD / rise
			}
			cs = append(cs, c)
		}
		sort.Slice(cs, func(i, j int) bool {
			return cmp.Or(cmp.Compare(cs[j].DeltaUSD, cs[i].DeltaUSD), cmp.Compare(cs[i].Value, cs[j].Value)) < 0
		})
		out[dim] = cs
	}
	return out
}

// rank keeps the top contributors of each dimension and orders them by
// impact, coarse dimensions first on a tie.
func rank(all map[string][]Contributor) []Contributor {
	order := map[string]int{}
	for i, d := range dims {
		order[d] = i
	}
	var out []Contributor
	for _, dim := range dims {
		cs := all[dim]
		out = append(out, cs[:min(len(cs), perDimension)]...)
	}
	slices.SortStableFunc(out, func(x, y Contributor) int {
		return cmp.Or(cmp.Compare(y.DeltaUSD, x.DeltaUSD), cmp.Compare(order[x.Dimension], order[y.Dimension]))
	})
	return out
}

// The bars a named cause must clear.
const (
	// causeShare is the share of the rise a single value must carry to
	// be named the cause.
	causeShare = 0.6
	// rareShare is the most of the history's tokens a model may have had
	// and still be "new".
	rareShare = 0.2
	// takeoverShare is the least of the day's tokens it must take.
	takeoverShare = 0.4
	// pricier is how much more per token it must cost.
	pricier = 1.5
)

// name recognises the cause, most specific first.
func name(all map[string][]Contributor, today, past []Item, days int, rise float64, metric string, cfg Config) Cause {
	if c, ok := meteredOnPlan(today, past, days, rise, metric, cfg); ok {
		return c
	}
	if c, ok := pricierModel(all, today, past); ok {
		return c
	}
	if top := first(all[DimSession]); top != nil && top.Share >= causeShare && top.Value != NoSession {
		c := Cause{Kind: CauseRunawayLoop, Dimension: DimSession, Value: top.Value, DeltaUSD: top.DeltaUSD, Share: top.Share}
		for _, it := range today {
			if it.Session == top.Value && it.Repo != "" {
				c.Repo = it.Repo
				break
			}
		}
		c.Pattern = CauseRunawayLoop + ":" + orUnknown(c.Repo)
		return c
	}
	c := Cause{Kind: CauseSpike, Pattern: CauseSpike + ":" + metric}
	for _, dim := range []string{DimModel, DimBiller, DimRepo, DimTool} {
		if top := first(all[dim]); top != nil && top.Share >= causeShare {
			c.Dimension, c.Value, c.DeltaUSD, c.Share = dim, top.Value, top.DeltaUSD, top.Share
			c.Pattern += ":" + dim + "=" + top.Value
			break
		}
	}
	return c
}

func first(cs []Contributor) *Contributor {
	if len(cs) == 0 {
		return nil
	}
	return &cs[0]
}

// meteredOnPlan is per-token spend, from a provider a plan covers,
// carrying the rise in spend.
func meteredOnPlan(today, past []Item, days int, rise float64, metric string, cfg Config) (Cause, bool) {
	if metric != MetricSpend {
		return Cause{}, false
	}
	byProvider := map[string]float64{}
	for _, it := range today {
		if !it.PlanCovered && cfg.PlanProviders[it.Provider] {
			byProvider[it.Provider] += it.CostUSD
		}
	}
	best, bestUSD := "", 0.0
	for p, usd := range byProvider {
		if usd > bestUSD || (usd == bestUSD && p < best) {
			best, bestUSD = p, usd
		}
	}
	if best == "" || rise <= 0 {
		return Cause{}, false
	}
	var base float64
	for _, it := range past {
		if it.Provider == best && !it.PlanCovered {
			base += it.CostUSD / float64(days)
		}
	}
	delta := bestUSD - base
	if delta/rise < causeShare {
		return Cause{}, false
	}
	c := Cause{Kind: CauseMeteredOnPlan, Dimension: DimBiller, Value: best, DeltaUSD: delta, Share: delta / rise,
		Pattern: CauseMeteredOnPlan + ":" + best}
	routes := map[string]float64{}
	for _, it := range today {
		if it.Provider == best && !it.PlanCovered && it.Endpoint != "" {
			routes[it.Endpoint] += it.CostUSD
		}
	}
	for e, usd := range routes {
		if usd > routes[c.Endpoint] || (usd == routes[c.Endpoint] && e < c.Endpoint) {
			c.Endpoint = e
		}
	}
	c.CoveragePct = coverage(today, best)
	c.BaselineCoveragePct = coverage(past, best)
	return c, true
}

// coverage is the share of provider's tokens a plan covered, the same
// measure as the scorecard's plan coverage.
func coverage(items []Item, provider string) *float64 {
	var plan, all int64
	for _, it := range items {
		if it.Provider != provider {
			continue
		}
		all += it.Tokens
		if it.PlanCovered {
			plan += it.Tokens
		}
	}
	if all == 0 {
		return nil
	}
	v := 100 * float64(plan) / float64(all)
	return &v
}

// pricierModel is a model rare in the history that took most of the
// day's tokens at a higher price per token than the history paid.
func pricierModel(all map[string][]Contributor, today, past []Item) (Cause, bool) {
	top := first(all[DimModel])
	if top == nil || top.Share < causeShare {
		return Cause{}, false
	}
	var dayTok, dayModelTok, pastTok, pastModelTok int64
	var dayModelUSD, pastUSD float64
	for _, it := range today {
		dayTok += it.Tokens
		if it.value(DimModel) == top.Value {
			dayModelTok += it.Tokens
			dayModelUSD += it.ValueUSD
		}
	}
	for _, it := range past {
		pastTok += it.Tokens
		pastUSD += it.ValueUSD
		if it.value(DimModel) == top.Value {
			pastModelTok += it.Tokens
		}
	}
	if dayTok == 0 || dayModelTok == 0 || pastTok == 0 || pastUSD == 0 {
		return Cause{}, false
	}
	price, basePrice := dayModelUSD/float64(dayModelTok)*1e6, pastUSD/float64(pastTok)*1e6
	if float64(pastModelTok)/float64(pastTok) > rareShare ||
		float64(dayModelTok)/float64(dayTok) < takeoverShare || price < pricier*basePrice {
		return Cause{}, false
	}
	return Cause{Kind: CausePricierModel, Dimension: DimModel, Value: top.Value, DeltaUSD: top.DeltaUSD, Share: top.Share,
		Pattern: CausePricierModel + ":" + top.Value, USDPerMTok: price, BaselineUSDPerMTok: basePrice}, true
}

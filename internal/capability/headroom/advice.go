package headroom

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/planswitch"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// MaxAdviceDays is the longest period the plan advice replays, and
// DefaultAdviceDays the period it replays when none is asked for.
const (
	MaxAdviceDays     = 90
	DefaultAdviceDays = plans.AdviceDays
)

// AdviceOption is one way the period could have been paid for.
type AdviceOption = plans.Option

// Advice is one provider's replay, and ResetProjection where its longest
// window is headed by the reset.
type (
	Advice          = plans.Advice
	ResetProjection = plans.ResetProjection
)

// RateWords names the rate a forecast projects, in the words every surface
// uses for it (plans.RateWords).
func RateWords(basis string) string { return plans.RateWords(basis) }

// AdviceCacheTTL is how long the glance reuses one replay: the advice is
// about weeks of history, and an hour more changes nothing in it.
const AdviceCacheTTL = time.Hour

// AdviceQuery picks the period the plan advice replays.
type AdviceQuery struct {
	// Days is how far back; zero is plans.AdviceDays.
	Days int
	// Project also projects each plan's longest window to its reset
	// (plans.Advice.AtReset). The projection is live, so the glance's
	// hour-long cached replay leaves it out.
	Project bool
}

// AdviceResult is plan right-sizing advice for every bound plan whose tiers
// TokenOps can replay. It recommends; it never binds or switches a plan.
type AdviceResult struct {
	Since     time.Time
	Until     time.Time
	Days      int
	Providers []plans.Advice
	// Notes name bound plans that cannot be replayed, and why.
	Notes           []string
	Unconfigured    string
	StorageDisabled string
}

// AdvicePayload is the plan advice on the wire (ADR 0010 §4).
type AdvicePayload struct {
	Since     time.Time      `json:"since"`
	Until     time.Time      `json:"until"`
	Days      int            `json:"days"`
	Providers []plans.Advice `json:"providers"`
	Notes     []string       `json:"notes,omitempty"`
	Error     string         `json:"error,omitempty"`
	Hint      string         `json:"hint,omitempty"`
}

// Payload is r on the wire.
func (r AdviceResult) Payload() *AdvicePayload {
	switch {
	case r.Unconfigured != "":
		return &AdvicePayload{Error: ErrPlansUnconfigured, Hint: r.Unconfigured, Providers: []plans.Advice{}}
	case r.StorageDisabled != "":
		return &AdvicePayload{Error: ErrStorageDisabled, Hint: r.StorageDisabled, Providers: []plans.Advice{}}
	}
	p := &AdvicePayload{Since: r.Since, Until: r.Until, Days: r.Days, Providers: r.Providers, Notes: r.Notes}
	if p.Providers == nil {
		p.Providers = []plans.Advice{}
	}
	return p
}

// Advise replays the last q.Days of each bound plan's windows against the
// other tiers of its family, metered overflow and the API at list price
// (plans.Replay). The plan in force at each reading comes from the plan
// history (ADR 0008), so readings from before a switch are scaled by the
// plan they were taken on. It reads; it binds nothing.
func Advise(ctx context.Context, d Deps, q AdviceQuery, now time.Time) (AdviceResult, error) {
	days := q.Days
	if days == 0 {
		days = plans.AdviceDays
	}
	if days < 1 || days > MaxAdviceDays {
		return AdviceResult{}, fmt.Errorf("days must be between 1 and %d, not %d", MaxAdviceDays, days)
	}
	if d.Config == nil || len(d.Config.Plans) == 0 {
		return AdviceResult{Unconfigured: UnconfiguredHint}, nil
	}
	if d.Reader == nil {
		return AdviceResult{StorageDisabled: StorageDisabledHint}, nil
	}
	since := now.Add(-time.Duration(days) * 24 * time.Hour)
	res := AdviceResult{Since: since, Until: now, Days: days}
	var history plans.History
	if d.PlanHistory != nil {
		h, err := d.PlanHistory()
		if err != nil {
			return AdviceResult{}, fmt.Errorf("read plan history: %w", err)
		}
		history = h
	}
	envs, err := d.Reader.ReadEvents(ctx, eventschema.EventTypePrompt, since)
	if err != nil {
		return AdviceResult{}, err
	}
	historyFrom, err := historyStart(ctx, d.Reader, envs, since)
	if err != nil {
		return AdviceResult{}, err
	}
	var observed plans.ObservedLimits
	if d.Observed != nil {
		observed = d.Observed()
	}
	providers := make([]string, 0, len(d.Config.Plans))
	for p := range d.Config.Plans {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		name, _ := plans.ResolveAlias(d.Config.Plans[provider])
		in, note := replayInput(provider, name, history, envs, d.Price, since, now, observed)
		if note != "" {
			res.Notes = append(res.Notes, note)
			continue
		}
		in.HistoryFrom = historyFrom
		a := plans.Replay(in)
		if q.Project {
			a.AtReset = projectLongest(ctx, d.Reader, provider, envs, days, now)
		}
		res.Providers = append(res.Providers, a)
	}
	return res, nil
}

// historyStart is when the store's history begins within the period from
// since, the first event of any provider: a day with no event of one
// provider after it is a day that provider was not used, while a day
// before it says nothing. Zero when the store holds events from before
// since, or none at all. Only when the period's first event is more than
// a day after since does it count the events before since, through the
// store's index.
func historyStart(ctx context.Context, r Reader, envs []*eventschema.Envelope, since time.Time) (time.Time, error) {
	var first time.Time
	for _, e := range envs {
		if e != nil && (first.IsZero() || e.Timestamp.Before(first)) {
			first = e.Timestamp
		}
	}
	if first.IsZero() || first.Sub(since) <= 24*time.Hour {
		return time.Time{}, nil
	}
	counts, err := r.CountBySource(ctx, time.Time{}, since)
	if err != nil {
		return time.Time{}, fmt.Errorf("count events before the period: %w", err)
	}
	for _, n := range counts {
		if n > 0 {
			return time.Time{}, nil
		}
	}
	return first, nil
}

// projectLongest is where provider's longest plan window is headed by its
// reset, on the forecast glance shows. The events the replay read hold
// the newest reading when they reach back as far as the windows are read
// (two weeks); a shorter replay reads the store.
func projectLongest(ctx context.Context, r Reader, provider string, envs []*eventschema.Envelope, days int, now time.Time) *plans.ResetProjection {
	var events plans.EventReader = r
	if days >= vendorWindowDays {
		events = envelopes(envs)
	}
	windows := plans.VendorWindows(ctx, events, eventschema.Provider(provider), now)
	applyRecentRate(ctx, r, provider, windows, now)
	return plans.LongestProjection(windows, now)
}

// vendorWindowDays is how far back plans.VendorWindows reads.
const vendorWindowDays = 14

// envelopes answers a read of prompt events from the ones the replay
// already read, which are all of that type.
type envelopes []*eventschema.Envelope

func (es envelopes) ReadEvents(_ context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	if t != eventschema.EventTypePrompt {
		return nil, fmt.Errorf("only prompt events are held, not %s", t)
	}
	out := make([]*eventschema.Envelope, 0, len(es))
	for _, e := range es {
		if e != nil && !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

// replayInput gathers what one provider's replay reads, or says why it
// cannot be replayed.
func replayInput(provider, name string, h plans.History, envs []*eventschema.Envelope, price plans.Pricer, since, now time.Time,
	observed plans.ObservedLimits) (plans.ReplayInput, string) {
	base, _, ok := plans.TierOf(name)
	if !ok {
		return plans.ReplayInput{}, fmt.Sprintf("%s: %s has no stated usage multiple between tiers in the catalog, "+
			"so it cannot be replayed against them", provider, name)
	}
	family := plans.TierFamily(name)
	used := plans.ObservedTiers(family, observed)
	var current plans.Tier
	for i := range family {
		if family[i].Name != name {
			continue
		}
		periods := h.Periods(provider, now.Add(-time.Minute), now, name)
		if len(periods) > 0 {
			if p, fee := planswitch.PriceOf(periods[len(periods)-1]); fee && p.Source == "yours" && p.Monthly > 0 &&
				(p.Currency == "" || strings.EqualFold(p.Currency, "USD")) {
				family[i].MonthlyUSD, family[i].PriceSource = p.Monthly, "yours"
			}
		}
		current = family[i]
	}
	in := plans.ReplayInput{
		Provider: provider, Since: since, Until: now,
		Readings: plans.QuotaReadingsFrom(envs, provider, since, now),
		FactorAt: func(t time.Time, window time.Duration) (float64, bool) {
			plan := h.At(provider, t, name)
			b, f, ok := plans.WindowFactor(plan, window)
			if l, hit := observed.Factor(plan, window); hit && ok {
				f = l.ObservedFactor
			}
			return f, ok && b == base
		},
		Current: current, Candidates: family, Observed: used,
	}
	in.EarlyResets = plans.EarlyResetsIn(envs, eventschema.Provider(provider), since, now,
		func(from, to time.Time) bool { return h.ChangedBetween(provider, from, to) })
	if price != nil {
		in.ListValue = func(from, to time.Time) (float64, int) {
			return plans.ListValue(envs, provider, from, to, price)
		}
	}
	return in, ""
}

// AdviceCache keeps one replay for the glance, which the menu bar asks
// for every minute. The daemon and the MCP server hold one; nil replays on
// every glance.
type AdviceCache struct {
	mu  sync.Mutex
	at  time.Time
	key string
	res []plans.Advice
	ok  bool
	// refreshing is open while a background replay runs; failedAt is
	// when the last one failed.
	refreshing chan struct{}
	failedAt   time.Time
}

// glanceAdviceBudget bounds the glance's own replay. A replay that does
// not fit goes on in the background with adviceRefreshBudget, and a
// failed one is not tried again for adviceRetryAfter.
var glanceAdviceBudget = 4 * time.Second

const (
	adviceRefreshBudget = 5 * time.Minute
	adviceRetryAfter    = 10 * time.Minute
)

// NewAdviceCache is an empty cache.
func NewAdviceCache() *AdviceCache { return &AdviceCache{} }

// glanceAdvice is the default period's advice for the glance; nil when it
// cannot be computed.
func glanceAdvice(ctx context.Context, d Deps, now time.Time) []plans.Advice {
	if d.Config == nil || len(d.Config.Plans) == 0 || d.Reader == nil {
		return nil
	}
	key := adviceKey(d)
	c := d.AdviceCache
	if c == nil {
		// No cache to fill: the replay fits the budget or is left out.
		ctx, cancel := context.WithTimeout(ctx, glanceAdviceBudget)
		defer cancel()
		res, err := Advise(ctx, d, AdviceQuery{}, now)
		if err != nil {
			return nil
		}
		return res.Providers
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	held := c.ok && c.key == key
	if held && !now.Before(c.at) && now.Sub(c.at) < AdviceCacheTTL {
		return c.res
	}
	if c.refreshing != nil || (!c.failedAt.IsZero() && now.Sub(c.failedAt) < adviceRetryAfter && !now.Before(c.failedAt)) {
		return c.stale(held)
	}
	if !held {
		// Nothing to serve yet: try within the glance's budget first. A
		// month of events is the heaviest read in the glance.
		bctx, cancel := context.WithTimeout(ctx, glanceAdviceBudget)
		res, err := Advise(bctx, d, AdviceQuery{}, now)
		cancel()
		if err == nil {
			c.at, c.key, c.res, c.ok = now, key, res.Providers, true
			return res.Providers
		}
		if ctx.Err() != nil || !errors.Is(err, context.DeadlineExceeded) {
			c.failedAt = now
			return nil
		}
	}
	// The replay does not fit the glance: run it in the background, once,
	// rather than read the month every glance and throw it away.
	done := make(chan struct{})
	c.refreshing = done
	go func() {
		defer close(done)
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), adviceRefreshBudget)
		defer cancel()
		res, err := Advise(rctx, d, AdviceQuery{}, now)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.refreshing = nil
		if err != nil {
			c.failedAt = now
			return
		}
		c.at, c.key, c.res, c.ok, c.failedAt = now, key, res.Providers, true, time.Time{}
	}()
	return c.stale(held)
}

// adviceKey names what a replay depends on beyond time: the bound plans
// and the observed-limits overlay. A changed overlay is a different
// replay.
func adviceKey(d Deps) string {
	key := fmt.Sprint(d.Config.Plans)
	if d.Observed != nil {
		o := d.Observed()
		key += fmt.Sprint(o.Off, o.UpdatedAt.UnixNano())
	}
	return key
}

// CachedAdvice is the default period's advice for a caller that can wait
// for it, such as the team upload: the replay d.AdviceCache holds when it
// is under AdviceCacheTTL old, otherwise a new one, which the glance then
// reuses. The upload replayed a month of events every hour on its own,
// beside the glance's replay of the same month. Without a cache it
// replays.
func CachedAdvice(ctx context.Context, d Deps, now time.Time) ([]plans.Advice, error) {
	c := d.AdviceCache
	if c == nil || d.Config == nil || len(d.Config.Plans) == 0 || d.Reader == nil {
		res, err := Advise(ctx, d, AdviceQuery{}, now)
		return res.Providers, err
	}
	key := adviceKey(d)
	c.mu.Lock()
	for {
		if c.ok && c.key == key && !now.Before(c.at) && now.Sub(c.at) < AdviceCacheTTL {
			res := c.res
			c.mu.Unlock()
			return res, nil
		}
		running := c.refreshing
		if running == nil {
			break
		}
		// The glance's replay is under way: its result serves here too.
		c.mu.Unlock()
		select {
		case <-running:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c.mu.Lock()
	}
	done := make(chan struct{})
	c.refreshing = done
	c.mu.Unlock()
	res, err := Advise(ctx, d, AdviceQuery{}, now)
	c.mu.Lock()
	c.refreshing = nil
	if err == nil {
		c.at, c.key, c.res, c.ok, c.failedAt = now, key, res.Providers, true, time.Time{}
	}
	c.mu.Unlock()
	close(done)
	return res.Providers, err
}

// stale is the advice held, served while a refresh runs. c.mu is held.
func (c *AdviceCache) stale(held bool) []plans.Advice {
	if !held {
		return nil
	}
	return c.res
}

package headroom

import (
	"context"
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

// AdviceCacheTTL is how long the glance reuses one replay: the advice is
// about weeks of history, and an hour more changes nothing in it.
const AdviceCacheTTL = time.Hour

// AdviceQuery picks the period the plan advice replays.
type AdviceQuery struct {
	// Days is how far back; zero is plans.AdviceDays.
	Days int
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
	providers := make([]string, 0, len(d.Config.Plans))
	for p := range d.Config.Plans {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		name, _ := plans.ResolveAlias(d.Config.Plans[provider])
		in, note := replayInput(provider, name, history, envs, d.Price, since, now)
		if note != "" {
			res.Notes = append(res.Notes, note)
			continue
		}
		res.Providers = append(res.Providers, plans.Replay(in))
	}
	return res, nil
}

// replayInput gathers what one provider's replay reads, or says why it
// cannot be replayed.
func replayInput(provider, name string, h plans.History, envs []*eventschema.Envelope, price plans.Pricer, since, now time.Time) (plans.ReplayInput, string) {
	base, _, ok := plans.TierOf(name)
	if !ok {
		return plans.ReplayInput{}, fmt.Sprintf("%s: %s has no stated usage multiple between tiers in the catalog, "+
			"so it cannot be replayed against them", provider, name)
	}
	family := plans.TierFamily(name)
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
			b, f, ok := plans.WindowFactor(h.At(provider, t, name), window)
			return f, ok && b == base
		},
		Current: current, Candidates: family,
	}
	in.EarlyResets = plans.EarlyResetsIn(envs, eventschema.Provider(provider), since, now,
		func(from, to time.Time) bool { return h.ChangedBetween(provider, from, to) })
	if price != nil {
		in.ListValueUSD, in.UnpricedRequests = plans.ListValue(envs, provider, since, now, price)
		in.ListValueKnown = true
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
}

// glanceAdviceBudget bounds the glance's replay.
const glanceAdviceBudget = 4 * time.Second

// NewAdviceCache is an empty cache.
func NewAdviceCache() *AdviceCache { return &AdviceCache{} }

// glanceAdvice is the default period's advice for the glance; nil when it
// cannot be computed.
func glanceAdvice(ctx context.Context, d Deps, now time.Time) []plans.Advice {
	if d.Config == nil || len(d.Config.Plans) == 0 || d.Reader == nil {
		return nil
	}
	key := fmt.Sprint(d.Config.Plans)
	c := d.AdviceCache
	if c != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.ok && c.key == key && !now.Before(c.at) && now.Sub(c.at) < AdviceCacheTTL {
			return c.res
		}
	}
	// A month of events is the heaviest read in the glance; on a busy
	// machine the glance goes out without the advice rather than wait.
	ctx, cancel := context.WithTimeout(ctx, glanceAdviceBudget)
	defer cancel()
	res, err := Advise(ctx, d, AdviceQuery{}, now)
	if err != nil {
		return nil
	}
	if c != nil {
		c.at, c.key, c.res, c.ok = now, key, res.Providers, true
	}
	return res.Providers
}

package plans

import (
	"context"
	"sort"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Allowance is capacity a vendor grants on top of the plan's windows that
// expires on its own: a banked reset credit, a temporary boost. Unused by
// its expiry, it is gone, which is what the lapse finding warns about.
//
// Today one source reports them: Codex's app server, whose
// account/rateLimits/read lists the account's rate-limit reset credits
// with when each expires. A reader for another vendor reports its own in
// the same reading attributes (AllowanceAttributes), and every surface
// picks them up.
type Allowance struct {
	// Kind is what the allowance is: AllowanceResetCredit, or a vendor's
	// own word for a boost.
	Kind string `json:"kind"`
	// ID is the vendor's identifier, so one allowance is traced across
	// readings; Title its own name for it, when it gives one.
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
	// GrantedAt is when the vendor granted it; ExpiresAt when it lapses
	// unused. A zero ExpiresAt does not expire, or the vendor did not say.
	GrantedAt time.Time `json:"granted_at,omitzero"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	// UsedPct is how much of a boost with its own share is used; nil for
	// a credit, which is spent whole or not at all.
	UsedPct *float64 `json:"used_pct,omitempty"`
	// Source is the reader that reported it, and ObservedAt when.
	Source     string    `json:"source,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
}

// AllowanceResetCredit is a banked reset: redeemed, it resets the plan's
// usage windows; unredeemed by its expiry, it lapses.
const AllowanceResetCredit = "reset-credit"

// Allowances is what the newest reading said a provider has banked.
type Allowances struct {
	// Available is how many the vendor says are available, which can
	// exceed len(Items): a vendor may report the count without the
	// details, or cap the list.
	Available int         `json:"available"`
	Items     []Allowance `json:"items,omitempty"`
}

// allowancesKey is the reading attribute that says a reader reports
// allowances at all; allowance_<n>_* hold each one.
const allowancesKey = "allowances_available"

// AllowanceAttributes is the reading attributes a reader writes for what
// a vendor reported banked: the count, then each allowance it detailed.
func AllowanceAttributes(available int, items []Allowance) map[string]string {
	out := map[string]string{allowancesKey: strconv.Itoa(available)}
	for i, a := range items {
		k := "allowance_" + strconv.Itoa(i) + "_"
		out[k+"kind"] = a.Kind
		if a.ID != "" {
			out[k+"id"] = a.ID
		}
		if a.Title != "" {
			out[k+"title"] = a.Title
		}
		if !a.GrantedAt.IsZero() {
			out[k+"granted_at"] = a.GrantedAt.UTC().Format(time.RFC3339)
		}
		if !a.ExpiresAt.IsZero() {
			out[k+"expires_at"] = a.ExpiresAt.UTC().Format(time.RFC3339)
		}
		if a.UsedPct != nil {
			out[k+"used_pct"] = strconv.FormatFloat(*a.UsedPct, 'f', 2, 64)
		}
	}
	return out
}

// parseAllowances reads the attributes AllowanceAttributes wrote; ok is
// false for a reading that reports no allowances at all.
func parseAllowances(attrs map[string]string) (Allowances, bool) {
	raw, ok := attrs[allowancesKey]
	if !ok {
		return Allowances{}, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return Allowances{}, false
	}
	out := Allowances{Available: n}
	for i := 0; ; i++ {
		k := "allowance_" + strconv.Itoa(i) + "_"
		kind, ok := attrs[k+"kind"]
		if !ok {
			break
		}
		a := Allowance{Kind: kind, ID: attrs[k+"id"], Title: attrs[k+"title"]}
		a.GrantedAt, _ = time.Parse(time.RFC3339, attrs[k+"granted_at"])
		a.ExpiresAt, _ = time.Parse(time.RFC3339, attrs[k+"expires_at"])
		if v, err := strconv.ParseFloat(attrs[k+"used_pct"], 64); err == nil {
			a.UsedPct = &v
		}
		out.Items = append(out.Items, a)
	}
	return out, true
}

// allowanceLookback is how far back the newest allowance reading is
// looked for. Allowances are the account's state now, read every quarter
// hour; a day-old reading says too little about a credit since redeemed.
const allowanceLookback = 24 * time.Hour

// VendorAllowances is what the newest reading for provider in the last day
// says is banked, with expired allowances left out. nil when no reader
// reports allowances for provider.
func VendorAllowances(ctx context.Context, reader EventReader, provider eventschema.Provider, now time.Time) *Allowances {
	events, err := reader.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-allowanceLookback))
	if err != nil {
		return nil
	}
	var best *eventschema.Envelope
	for _, e := range events {
		if e == nil || e.Attributes[allowancesKey] == "" || readingProvider(e) != provider {
			continue
		}
		if best == nil || e.Timestamp.After(best.Timestamp) {
			best = e
		}
	}
	if best == nil {
		return nil
	}
	a, ok := parseAllowances(best.Attributes)
	if !ok {
		return nil
	}
	live := a.Items[:0]
	for _, it := range a.Items {
		if !it.ExpiresAt.IsZero() && !it.ExpiresAt.After(now) {
			a.Available = max(0, a.Available-1)
			continue
		}
		it.Source, it.ObservedAt = best.Source, best.Timestamp
		live = append(live, it)
	}
	a.Items = live
	sort.SliceStable(a.Items, func(i, j int) bool { return expiresBefore(a.Items[i], a.Items[j]) })
	return &a
}

// expiresBefore orders allowances soonest to expire first; one that does
// not expire goes last.
func expiresBefore(a, b Allowance) bool {
	switch {
	case a.ExpiresAt.IsZero():
		return false
	case b.ExpiresAt.IsZero():
		return true
	}
	return a.ExpiresAt.Before(b.ExpiresAt)
}

// ExpiringWithin is the allowances that lapse within d of now, soonest
// first: the ones worth using before they go.
func (a *Allowances) ExpiringWithin(now time.Time, d time.Duration) []Allowance {
	if a == nil {
		return nil
	}
	var out []Allowance
	for _, it := range a.Items {
		if !it.ExpiresAt.IsZero() && it.ExpiresAt.After(now) && it.ExpiresAt.Sub(now) <= d {
			out = append(out, it)
		}
	}
	return out
}

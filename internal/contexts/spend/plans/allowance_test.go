package plans

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// What a reader writes for its banked allowances reads back: the count,
// each allowance, and the reading it came from. An expired allowance is
// left out, the newest reading wins, and another provider's is not read.
func TestVendorAllowancesReadsTheNewestReading(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	reading := func(at time.Time, provider eventschema.Provider, available int, items ...Allowance) *eventschema.Envelope {
		return &eventschema.Envelope{
			Source: "codex-app-server", Timestamp: at, Attributes: AllowanceAttributes(available, items),
			Payload: &eventschema.PromptEvent{Provider: provider},
		}
	}
	soon := Allowance{Kind: AllowanceResetCredit, ID: "rc-1", Title: "Reset", GrantedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(6 * time.Hour)}
	later := Allowance{Kind: AllowanceResetCredit, ID: "rc-2", ExpiresAt: now.Add(5 * 24 * time.Hour)}
	gone := Allowance{Kind: AllowanceResetCredit, ID: "rc-0", ExpiresAt: now.Add(-time.Minute)}
	forever := Allowance{Kind: AllowanceResetCredit, ID: "rc-3"}

	tests := []struct {
		name      string
		events    []*eventschema.Envelope
		provider  eventschema.Provider
		wantNil   bool
		available int
		ids       []string
	}{
		{name: "no reader reports any", events: []*eventschema.Envelope{{Source: "codex-app-server", Timestamp: now,
			Attributes: map[string]string{"primary_used_pct": "4"}, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI}}},
			provider: eventschema.ProviderOpenAI, wantNil: true},
		{name: "soonest first, expired left out", events: []*eventschema.Envelope{
			reading(now.Add(-time.Hour), eventschema.ProviderOpenAI, 4, forever, later, gone, soon)},
			provider: eventschema.ProviderOpenAI, available: 3, ids: []string{"rc-1", "rc-2", "rc-3"}},
		{name: "the newest reading wins", events: []*eventschema.Envelope{
			reading(now.Add(-2*time.Hour), eventschema.ProviderOpenAI, 1, soon),
			reading(now.Add(-time.Hour), eventschema.ProviderOpenAI, 0)},
			provider: eventschema.ProviderOpenAI, available: 0},
		{name: "count without details", events: []*eventschema.Envelope{reading(now, eventschema.ProviderOpenAI, 2)},
			provider: eventschema.ProviderOpenAI, available: 2},
		{name: "another provider's", events: []*eventschema.Envelope{reading(now, eventschema.ProviderOpenAI, 1, soon)},
			provider: eventschema.ProviderAnthropic, wantNil: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := VendorAllowances(context.Background(), authFakeReader{events: tc.events}, tc.provider, now)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("got %+v, want none", got)
				}
				return
			}
			if got == nil || got.Available != tc.available || len(got.Items) != len(tc.ids) {
				t.Fatalf("got %+v", got)
			}
			for i, id := range tc.ids {
				if got.Items[i].ID != id || got.Items[i].Source != "codex-app-server" {
					t.Errorf("item %d = %+v, want %s", i, got.Items[i], id)
				}
			}
		})
	}
}

// Only an allowance that lapses within the horizon is expiring; one with
// no expiry never is.
func TestAllowancesExpiringWithin(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	a := &Allowances{Available: 3, Items: []Allowance{
		{ID: "soon", ExpiresAt: now.Add(6 * time.Hour)},
		{ID: "later", ExpiresAt: now.Add(48 * time.Hour)},
		{ID: "forever"},
	}}
	got := a.ExpiringWithin(now, 24*time.Hour)
	if len(got) != 1 || got[0].ID != "soon" {
		t.Errorf("expiring = %+v", got)
	}
	var none *Allowances
	if none.ExpiringWithin(now, time.Hour) != nil {
		t.Error("nil allowances expire nothing")
	}
}

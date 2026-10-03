package headroom_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/fireworks"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type storeReader struct{ s *sqlite.Store }

func (r storeReader) CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error) {
	return r.s.CountBySource(ctx, since, until)
}

func (r storeReader) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	return r.s.ReadEvents(ctx, t, since)
}

// A Fireworks reading with no plan bound shows up as pay-as-you-go
// against the vendor's own cap, through the real store: nothing to set up.
func TestFireworksReadingBindsPayAsYouGo(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	env := fireworks.NewEnvelope(now.Add(-time.Minute), fireworks.Reading{
		Scope: fireworks.ScopeUser, AccountID: "acme", UsedUSD: 41.5, LimitUSD: 100,
	})
	if err := store.Append(ctx, env); err != nil {
		t.Fatal(err)
	}

	got, err := headroom.Compute(ctx, headroom.Deps{Config: cfgWith(nil), Reader: storeReader{store}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Reports) != 1 {
		t.Fatalf("reports %+v, unconfigured %q", got.Reports, got.Unconfigured)
	}
	r := got.Reports[0]
	if r.Provider != "fireworks" || r.PlanName != plans.PayAsYouGo || r.SpendSource != "vendor" ||
		r.SpendUSD != 41.5 || r.SpendLimitUSD != 100 || r.SpendPct != 41.5 {
		t.Errorf("report %+v", r)
	}
	if r.Note == "" {
		t.Error("an inferred binding should say how to change it")
	}

	// A plan the operator bound wins over the inference.
	got, err = headroom.Compute(ctx, headroom.Deps{Config: cfgWith(map[string]string{"fireworks": plans.PayAsYouGo}), Reader: storeReader{store}}, now)
	if err != nil || len(got.Reports) != 1 || got.Reports[0].Note != "" {
		t.Errorf("bound: %+v %v", got.Reports, err)
	}
}

// Usage billed per token shows each provider without setup, counting every
// turn the key was billed for, through any endpoint. A provider whose
// usage cost nothing is left out.
func TestMeteredUsageShowsAsPayAsYouGo(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	turn := func(id string, p eventschema.Provider, endpoint string, cost float64) *eventschema.Envelope {
		attrs := map[string]string{"granularity": "assistant_turn"}
		if endpoint != "" {
			attrs["endpoint"] = endpoint
		}
		return &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: now.Add(-time.Hour), Source: "test", Attributes: attrs,
			Payload: &eventschema.PromptEvent{Provider: p, CostUSD: cost, CostSource: eventschema.CostSourceMetered},
		}
	}
	if err := store.AppendBatch(ctx, []*eventschema.Envelope{
		turn("z1", "zai", "zai-api", 2),
		turn("a1", eventschema.ProviderAnthropic, "fireworks", 3), // Claude through FireRouter, on the API key
		turn("a2", eventschema.ProviderAnthropic, "", 1),
		turn("o1", "opencode", "opencode", 0), // a free model
	}); err != nil {
		t.Fatal(err)
	}
	got, err := headroom.Compute(ctx, headroom.Deps{Config: cfgWith(nil), Reader: storeReader{store}}, now)
	if err != nil {
		t.Fatal(err)
	}
	spend := map[string]float64{}
	for _, r := range got.Reports {
		spend[r.Provider] = r.SpendUSD
		if r.PlanName != plans.PayAsYouGo || r.Note == "" {
			t.Errorf("%s: plan %q note %q", r.Provider, r.PlanName, r.Note)
		}
	}
	if len(spend) != 2 || spend["zai"] != 2 || spend["anthropic"] != 4 {
		t.Errorf("spend by provider %v", spend)
	}

	// A bound plan keeps its own rules: Enterprise's limit counts only its
	// own endpoint, and zai stays inferred.
	got, err = headroom.Compute(ctx, headroom.Deps{Config: cfgWith(map[string]string{"anthropic": "claude-enterprise"}), Reader: storeReader{store}}, now)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, r := range got.Reports {
		if r.Provider == "anthropic" {
			seen = true
			if r.SpendUSD != 1 {
				t.Errorf("enterprise spend %v, want only the direct turn", r.SpendUSD)
			}
		}
	}
	if !seen || len(got.Notes) != 0 {
		t.Errorf("no anthropic report: %+v notes %v", got.Reports, got.Notes)
	}
}

// A prepaid vendor reports a balance but not spend: headroom keeps its
// usage estimate, shows the balance, and an empty balance is high risk.
func TestPrepaidBalanceShows(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		balance  string
		risk     string
		wantNote string
	}{
		"credit left": {"7.25", "", "prepaid: 7.25 USD left"},
		"used up":     {"0.00", plans.RiskHigh, "the vendor reports the prepaid balance as used up"},
	} {
		store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		reading := &eventschema.Envelope{
			ID: "r", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: now.Add(-time.Minute), Source: "deepseek-account",
			Attributes: map[string]string{
				"granularity": "quota_snapshot", "billing": "per_token", "extra_usage_limit": "0.00",
				"extra_usage_currency": "USD", "extra_usage_limit_reached": "false", "balance_usd": tc.balance,
			},
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderDeepSeek, Status: 200},
		}
		turn := &eventschema.Envelope{
			ID: "t", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: now.Add(-time.Hour), Source: "test", Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderDeepSeek, CostUSD: 1.5, CostSource: eventschema.CostSourceMetered},
		}
		if err := store.AppendBatch(ctx, []*eventschema.Envelope{reading, turn}); err != nil {
			t.Fatal(err)
		}
		got, err := headroom.Compute(ctx, headroom.Deps{Config: cfgWith(nil), Reader: storeReader{store}}, now)
		_ = store.Close()
		if err != nil || len(got.Reports) != 1 {
			t.Fatalf("%s: %+v %v", name, got, err)
		}
		r := got.Reports[0]
		if r.BalanceUSD == nil || r.SpendUSD != 1.5 || r.SpendSource != "estimate" || r.Note != tc.wantNote {
			t.Errorf("%s: %+v", name, r)
		}
		if tc.risk != "" && r.OverageRisk != tc.risk {
			t.Errorf("%s: risk %s", name, r.OverageRisk)
		}
	}
}

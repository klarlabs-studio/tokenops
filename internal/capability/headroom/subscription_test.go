package headroom_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// A coding plan the catalog does not know (Kimi Code) shows up with the
// windows its own reader reported, through the real store: the busiest
// window sets the risk, and the note says how to name the tier.
func TestSubscriptionReadingShowsTheVendorsWindows(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC()
	env := accounts.NewEnvelope(now.Add(-time.Minute), accounts.Kimi{}, accounts.Reading{
		Scope: "account", Subscription: true, Windows: []accounts.Window{
			{Name: "5h", UsedPct: 20, Duration: 5 * time.Hour, ResetsAt: now.Add(2 * time.Hour)},
			{Name: "week", UsedPct: 91, Duration: 7 * 24 * time.Hour, ResetsAt: now.Add(48 * time.Hour)},
		}})
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
	if r.Provider != "kimi" || r.PlanName != plans.Subscription || r.Note == "" {
		t.Errorf("report %+v", r)
	}
	if len(r.Windows) != 2 || r.Windows[0].Name != "week" || r.Windows[0].UsedPct != 91 || r.Windows[0].ResetsIn == "" {
		t.Errorf("windows %+v", r.Windows)
	}
	if r.OverageRisk != plans.RiskHigh {
		t.Errorf("risk %s, want high from the 91%% week", r.OverageRisk)
	}
}

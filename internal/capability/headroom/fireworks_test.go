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

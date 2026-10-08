package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var singleReadBase = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

// seedUsageMix stores n prompt events built to catch every way the single
// read could drift from the three statements it replaced: NULL and empty
// providers and models (SQLite grouped them apart), every cost source
// including a non-text one, stored costs whose float sum depends on the
// order it adds in, whole-number costs, many events sharing a timestamp,
// legacy cache attributes, readings without usage, the activity-proxy
// source, workflow and agent ids, and events outside the window.
func seedUsageMix(t *testing.T, s *Store, n int, seed int64) {
	t.Helper()
	ctx := context.Background()
	rng := rand.New(rand.NewSource(seed))
	providers := []eventschema.Provider{eventschema.ProviderAnthropic, eventschema.ProviderOpenAI, ""}
	models := []string{"claude-opus-4-7", "gpt-4o", "gpt-4o-mini", "no-such-model", ""}
	sources := []eventschema.CostSource{"", eventschema.CostSourceMetered, eventschema.CostSourcePlanIncluded, eventschema.CostSourceTrial}
	envs := make([]*eventschema.Envelope, 0, n)
	for i := range n {
		// Ten days around the windows, at minute grain so many share one.
		at := singleReadBase.Add(time.Duration(rng.Intn(10*24*60)) * time.Minute)
		p := &eventschema.PromptEvent{
			Provider:     providers[rng.Intn(len(providers))],
			RequestModel: models[rng.Intn(len(models))],
			InputTokens:  int64(rng.Intn(5000)),
			OutputTokens: int64(rng.Intn(800)),
			CostSource:   sources[rng.Intn(len(sources))],
		}
		p.TotalTokens = p.InputTokens + p.OutputTokens
		if rng.Intn(3) == 0 {
			p.CachedInputTokens = p.InputTokens / 2
			p.CacheWriteInputTokens = p.InputTokens / 4
			p.CacheWrite1hInputTokens = p.InputTokens / 8
		}
		switch rng.Intn(6) {
		case 0, 1:
			// No stored cost.
		case 2:
			p.CostUSD = float64(rng.Intn(4) + 1) // whole dollars
		case 3:
			// Magnitudes far apart, so the compensated sum depends on
			// the order it adds in.
			p.CostUSD = (rng.Float64() - 0.5) * []float64{1e-9, 1e-3, 1, 1e7, 1e15}[rng.Intn(5)]
		default:
			p.CostUSD = rng.Float64() / 100
		}
		if rng.Intn(10) == 0 {
			// A reading: no model, no tokens.
			p.RequestModel, p.InputTokens, p.OutputTokens, p.TotalTokens = "", 0, 0, 0
		}
		if rng.Intn(4) == 0 {
			p.WorkflowID = fmt.Sprintf("wf-%d", rng.Intn(3))
		}
		if rng.Intn(4) == 0 {
			p.AgentID = fmt.Sprintf("agent-%d", rng.Intn(3))
		}
		env := &eventschema.Envelope{
			ID: fmt.Sprintf("e-%05d", i), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: at, Source: "claude-code-jsonl", Payload: p,
		}
		if rng.Intn(15) == 0 {
			env.Source = "mcp-session"
		}
		if rng.Intn(8) == 0 {
			env.Attributes = map[string]string{"cache_read_input": fmt.Sprint(p.InputTokens / 3), "cache_creation_input": "7"}
		}
		envs = append(envs, env)
	}
	// A burst at one instant whose costs cancel around a huge pair: the
	// compensated sum keeps the small ones in its error term, which then
	// rounds differently in each order. Events sharing a timestamp are
	// walked by the index's next columns, so input_tokens fixes the order.
	burst := singleReadBase.Add(30*time.Hour + 17*time.Minute)
	for i := range 60 {
		cost := []float64{1e15 * rng.Float64(), rng.Float64(), 1e-3 * rng.Float64()}[i%3]
		switch i {
		case 0:
			cost = 1e31
		case 30:
			cost = -1e31
		}
		envs = append(envs, &eventschema.Envelope{
			ID: fmt.Sprintf("burst-%02d", i), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: burst, Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-4-7",
				InputTokens: int64(1000 + i), OutputTokens: 1, TotalTokens: int64(1001 + i), CostUSD: cost,
			},
		})
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// What the store's writers never produce but a hand-edited or older
	// store may hold: empty strings beside NULLs, a non-text cost source,
	// and a stored cost of exactly zero.
	for _, q := range []string{
		`UPDATE events SET provider = '' WHERE rowid % 13 = 0`,
		`UPDATE events SET model = '' WHERE rowid % 11 = 0`,
		`UPDATE events SET payload = json_set(payload, '$.cost_source', 5) WHERE rowid % 17 = 0`,
		`UPDATE events SET cost_usd = 0 WHERE rowid % 19 = 0`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Another type in the window, which no rollup reads.
	wf := &eventschema.Envelope{
		ID: "wf-event", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypeWorkflow,
		Timestamp: singleReadBase.Add(time.Hour), Source: "proxy",
		Payload: &eventschema.WorkflowEvent{WorkflowID: "wf-0", CumulativeInputTokens: 99, CumulativeCostUSD: 3},
	}
	if err := s.Append(ctx, wf); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
}

// singleReadFilters are the windows the equality tests read.
func singleReadFilters() map[string]analytics.Filter {
	since := singleReadBase.Add(24 * time.Hour)
	return map[string]analytics.Filter{
		"everything":   {},
		"window":       {Since: since},
		"bounded":      {Since: since, Until: since.Add(5 * 24 * time.Hour)},
		"provider":     {Provider: "anthropic", Since: since},
		"model":        {Provider: "openai", Model: "gpt-4o"},
		"workflow":     {WorkflowID: "wf-1"},
		"agent":        {AgentID: "agent-2", Since: since},
		"re-admitted":  {Since: since, IncludeSources: []string{"mcp-session"}},
		"all sources":  {ExcludeSources: []string{}},
		"empty window": {Since: singleReadBase.Add(100 * 24 * time.Hour)},
	}
}

var singleReadGroups = []analytics.Group{analytics.GroupNone, analytics.GroupProvider, analytics.GroupModel, analytics.GroupWorkflow, analytics.GroupAgent}

// sameBytes fails unless got and want are equal and encode to the same
// JSON: a float off in its last bit encodes differently.
func sameBytes(t *testing.T, what string, got, want any) {
	t.Helper()
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%s: marshal: %v", what, err)
	}
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("%s: marshal: %v", what, err)
	}
	if string(g) != string(w) || !reflect.DeepEqual(got, want) {
		t.Errorf("%s differs from the three-statement answer:\n got %s\nwant %s", what, g, w)
	}
}

// TestSingleReadEqualsTheThreeStatements holds BucketUsage and WindowUsage
// to what the three statements each replaced answered, bit for bit.
func TestSingleReadEqualsTheThreeStatements(t *testing.T) {
	ctx := context.Background()
	for _, seed := range []int64{1, 2} {
		s := newTestStore(t)
		seedUsageMix(t, s, 3000, seed)
		oracle := oracleStore{s}
		for name, f := range singleReadFilters() {
			got, err := s.WindowUsage(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			want, err := oracle.WindowUsage(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			sameBytes(t, fmt.Sprintf("seed %d %s: WindowUsage", seed, name), got, want)
			for _, g := range singleReadGroups {
				for _, width := range []int64{3600, 86400} {
					got, err := s.BucketUsage(ctx, f, width, g)
					if err != nil {
						t.Fatal(err)
					}
					want, err := oracle.BucketUsage(ctx, f, width, g)
					if err != nil {
						t.Fatal(err)
					}
					sameBytes(t, fmt.Sprintf("seed %d %s group %q width %d: BucketUsage", seed, name, g, width), got, want)
				}
			}
		}
	}
}

// TestSingleReadAnswersTheSameRollups holds the aggregator's answers —
// what /api/spend/summary and /api/spend/series send — to the ones it gave
// over the three statements, byte for byte.
func TestSingleReadAnswersTheSameRollups(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUsageMix(t, s, 4000, 7)
	eng := spend.NewEngine(spend.DefaultTable())
	for _, engine := range []*spend.Engine{eng, nil} {
		got, want := analytics.New(s, engine), analytics.New(oracleStore{s}, engine)
		for name, f := range singleReadFilters() {
			gs, err := got.Summarize(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			ws, err := want.Summarize(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			sameBytes(t, name+": Summarize", gs, ws)
			for _, g := range singleReadGroups {
				for _, b := range []analytics.Bucket{analytics.BucketHour, analytics.BucketDay} {
					gr, err := got.AggregateBy(ctx, f, b, g)
					if err != nil {
						t.Fatal(err)
					}
					wr, err := want.AggregateBy(ctx, f, b, g)
					if err != nil {
						t.Fatal(err)
					}
					sameBytes(t, fmt.Sprintf("%s %s %q: AggregateBy", name, b, g), gr, wr)
				}
			}
		}
	}
}

// TestSeededCostsDependOnTheirOrder proves the equality tests above can
// fail: the seeded stored costs sum to a different float when added in
// another order, so a single read visiting events out of SQLite's order
// would not match.
func TestSeededCostsDependOnTheirOrder(t *testing.T) {
	s := newTestStore(t)
	seedUsageMix(t, s, 3000, 1)
	var costs []float64
	err := s.scanUsage(context.Background(), analytics.Filter{}, analytics.GroupNone, func(r *usageRow) {
		if r.cost.Valid {
			costs = append(costs, r.cost.Float64)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := func(xs []float64) float64 {
		var s sqlSum
		for _, x := range xs {
			s.add(x)
		}
		return s.value()
	}
	forward := sum(costs)
	slices.Reverse(costs)
	reversed := sum(costs)
	slices.Sort(costs)
	sorted := sum(costs)
	if forward == reversed && forward == sorted {
		t.Fatalf("seeded costs sum to %v in every order; the equality tests could not see an order change", forward)
	}
}

// TestSQLSumMatchesSQLite pins sqlSum to SQLite's own SUM, including the
// cases it treats apart: no values, integers, and an overflowing
// compensation.
func TestSQLSumMatchesSQLite(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rng := rand.New(rand.NewSource(3))
	const random = 50
	cases := make([][]float64, 0, 6+random)
	cases = append(cases,
		nil,
		[]float64{0},
		[]float64{1, 2, 3},
		[]float64{1e308, 1e308, -1e308},
		[]float64{0.1, 0.2, 0.3},
		[]float64{1e16, 1, -1e16, 1},
	)
	for range random {
		xs := make([]float64, rng.Intn(200)+1)
		for i := range xs {
			xs[i] = (rng.Float64() - 0.5) * []float64{1e-12, 1, 1e9, 1e17}[rng.Intn(4)]
		}
		cases = append(cases, xs)
	}
	for i, xs := range cases {
		args := make([]any, len(xs))
		q := `SELECT COALESCE(SUM(x), 0) FROM (SELECT NULL AS x WHERE 0`
		for j, x := range xs {
			args[j] = x
			q += ` UNION ALL SELECT CAST(? AS REAL)`
		}
		q += `)`
		var want float64
		if err := s.db.QueryRowContext(ctx, q, args...).Scan(&want); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		var got sqlSum
		for _, x := range xs {
			got.add(x)
		}
		if g := got.value(); g != want && !(g != g && want != want) {
			t.Errorf("case %d: sqlSum %v, SQLite %v", i, g, want)
		}
	}
}

// TestSingleReadOnARealStore compares the single read with the three
// statements on a copy of a real store, when TOKENOPS_ROLLUP_STORE names
// one. It never writes to the store beyond what opening it migrates, so
// point it at a copy.
func TestSingleReadOnARealStore(t *testing.T) {
	path := os.Getenv("TOKENOPS_ROLLUP_STORE")
	if path == "" {
		t.Skip("TOKENOPS_ROLLUP_STORE not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	eng := spend.NewEngine(spend.DefaultTable())
	got, want := analytics.New(s, eng), analytics.New(oracleStore{s}, eng)
	now := time.Now()
	for _, back := range []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour, 365 * 24 * time.Hour} {
		f := analytics.Filter{Since: now.Add(-back)}
		gs, err := got.Summarize(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		ws, err := want.Summarize(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		sameBytes(t, fmt.Sprintf("%v: Summarize", back), gs, ws)
		for _, g := range singleReadGroups {
			for _, b := range []analytics.Bucket{analytics.BucketHour, analytics.BucketDay} {
				gr, err := got.AggregateBy(ctx, f, b, g)
				if err != nil {
					t.Fatal(err)
				}
				wr, err := want.AggregateBy(ctx, f, b, g)
				if err != nil {
					t.Fatal(err)
				}
				sameBytes(t, fmt.Sprintf("%v %s %q: AggregateBy", back, b, g), gr, wr)
			}
		}
	}
}

// BenchmarkRollupsOnARealStore times a month's summary and daily series,
// single read against three statements, on TOKENOPS_ROLLUP_STORE.
func BenchmarkRollupsOnARealStore(b *testing.B) {
	path := os.Getenv("TOKENOPS_ROLLUP_STORE")
	if path == "" {
		b.Skip("TOKENOPS_ROLLUP_STORE not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, path, Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	eng := spend.NewEngine(spend.DefaultTable())
	f := analytics.Filter{Since: time.Now().Add(-30 * 24 * time.Hour)}
	for _, impl := range []struct {
		name  string
		store analytics.Store
	}{{"single-read", s}, {"three-statements", oracleStore{s}}} {
		agg := analytics.New(impl.store, eng)
		b.Run("summary/"+impl.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := agg.Summarize(ctx, f); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("series/"+impl.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := agg.AggregateBy(ctx, f, analytics.BucketDay, analytics.GroupNone); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

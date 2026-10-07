package spending

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestWindowOf(t *testing.T) {
	w, err := WindowOf(WindowQuery{Since: "2026-01-01T00:00:00Z", Until: "2026-01-02T00:00:00Z", WorkflowID: "wf"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if w.WorkflowID != "wf" || !w.Since.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("window = %+v", w)
	}
	if _, err := WindowOf(WindowQuery{Since: "nope"}, time.Hour); err == nil {
		t.Error("an unparsable since should be refused")
	}
	w, err = WindowOf(WindowQuery{}, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(w.Since); age < time.Hour || age > 3*time.Hour {
		t.Errorf("default since is %v ago, want about 2h", age)
	}
}

func TestHorizonDays(t *testing.T) {
	tests := map[string]int{"": 7, "3": 3, "30": 30, "31": 7, "0": 7, "-2": 7, "x": 7}
	for in, want := range tests {
		if got := HorizonDays(in); got != want {
			t.Errorf("HorizonDays(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSeriesParsesBucketAndGroup(t *testing.T) {
	tests := []struct {
		bucket, group string
		wantB         analytics.Bucket
		wantG         analytics.Group
	}{
		{"", "", analytics.BucketHour, analytics.GroupNone},
		{"DAY", "Model", analytics.BucketDay, analytics.GroupModel},
		{"week", "workflow", analytics.BucketHour, analytics.GroupWorkflow},
		{"day", "agent", analytics.BucketDay, analytics.GroupAgent},
		{"hour", "provider", analytics.BucketHour, analytics.GroupProvider},
		{"hour", "colour", analytics.BucketHour, analytics.GroupNone},
	}
	for _, tt := range tests {
		got, err := Series(context.Background(), &fakeAgg{}, Window{}, tt.bucket, tt.group, "USD")
		if err != nil {
			t.Fatal(err)
		}
		if got.Bucket != tt.wantB || got.Group != tt.wantG {
			t.Errorf("Series(%q, %q) = %v/%v, want %v/%v", tt.bucket, tt.group, got.Bucket, got.Group, tt.wantB, tt.wantG)
		}
	}
}

func TestWorkflowsRollsUpPerWorkflow(t *testing.T) {
	agg := &fakeAgg{rows: []analytics.Row{
		{GroupKey: "wf-a", Requests: 2, TotalTokens: 100, CostUSD: 1},
		{GroupKey: "wf-a", Requests: 1, TotalTokens: 50, CostUSD: 0.5},
		{GroupKey: "wf-b", Requests: 1, TotalTokens: 10, CostUSD: 0.1},
		{GroupKey: "", Requests: 9, TotalTokens: 900, CostUSD: 9},
	}}
	got, err := Workflows(context.Background(), agg, Window{}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got.Workflows, func(i, j int) bool { return got.Workflows[i].WorkflowID < got.Workflows[j].WorkflowID })
	want := []WorkflowTotal{{"wf-a", 3, 150, 1.5}, {"wf-b", 1, 10, 0.1}}
	if len(got.Workflows) != len(want) || got.Workflows[0] != want[0] || got.Workflows[1] != want[1] {
		t.Errorf("workflows = %+v, want %+v", got.Workflows, want)
	}
}

func TestForecastReadsTheLastThirtyDays(t *testing.T) {
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	agg := &fakeAgg{}
	got, err := Forecast(context.Background(), agg, 3, "USD", now)
	if err != nil {
		t.Fatal(err)
	}
	if !agg.got.Since.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Errorf("since = %v", agg.got.Since)
	}
	if got.HorizonDays != 3 || got.Currency != "USD" {
		t.Errorf("report = %+v", got)
	}
}

// The rollups replaced maps the daemon encoded, whose keys come out
// sorted; the structs must keep that order on the wire.
func TestRollupFieldsKeepTheirWireOrder(t *testing.T) {
	for _, v := range []any{SummaryReport{}, SeriesReport{}, ForecastReport{}, CacheReport{}, WorkflowTotals{}, Optimizations{}} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		dec := json.NewDecoder(bytes.NewReader(raw))
		_, _ = dec.Token()
		for dec.More() {
			tok, _ := dec.Token()
			keys = append(keys, tok.(string))
			var skip json.RawMessage
			_ = dec.Decode(&skip)
		}
		if !sort.StringsAreSorted(keys) {
			t.Errorf("%T keys %v are not in sorted order", v, keys)
		}
	}
}

type fakeEvents struct {
	envs []*eventschema.Envelope
	got  sqlite.Filter
	err  error
}

func (f *fakeEvents) Query(_ context.Context, flt sqlite.Filter) ([]*eventschema.Envelope, error) {
	f.got = flt
	return f.envs, f.err
}

func TestOptimizationsIn(t *testing.T) {
	at := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	q := &fakeEvents{envs: []*eventschema.Envelope{
		{Timestamp: at, Payload: &eventschema.OptimizationEvent{Kind: "k", Mode: "passive", Decision: "applied", EstimatedSavingsTokens: 5, WorkflowID: "wf"}},
		{Timestamp: at, Payload: &eventschema.PromptEvent{}},
	}}
	got, err := OptimizationsIn(context.Background(), q, Window{WorkflowID: "wf", AgentID: "a", Since: at}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Optimizations) != 1 || got.Optimizations[0].EstimatedSavingsTokens != 5 {
		t.Errorf("optimizations = %+v", got.Optimizations)
	}
	if q.got.Type != eventschema.EventTypeOptimization || q.got.Limit != maxOptimizations || q.got.WorkflowID != "wf" || q.got.AgentID != "a" {
		t.Errorf("query = %+v", q.got)
	}
	q.err = errors.New("disk")
	if _, err := OptimizationsIn(context.Background(), q, Window{}, "USD"); err == nil {
		t.Error("a store failure should surface")
	}
}

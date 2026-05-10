package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

func newRecorder(t *testing.T) *Recorder {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	store, err := sqlite.Open(context.Background(), path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewRecorder(store)
}

func TestRecordAndQueryRoundTrip(t *testing.T) {
	rec := newRecorder(t)
	ctx := context.Background()
	got, err := rec.Record(ctx, Entry{
		Action: ActionConfigChange,
		Actor:  "felix@example",
		Target: "config.yaml",
		Details: map[string]any{
			"path":     "tls.enabled",
			"oldValue": false,
			"newValue": true,
		},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got.ID == "" {
		t.Error("ID not minted")
	}
	if got.Timestamp.IsZero() {
		t.Error("timestamp not set")
	}
	entries, err := rec.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	e := entries[0]
	if e.Action != ActionConfigChange || e.Actor != "felix@example" || e.Target != "config.yaml" {
		t.Errorf("entry mismatch: %+v", e)
	}
	if e.Details["path"] != "tls.enabled" {
		t.Errorf("details lost: %+v", e.Details)
	}
}

func TestQueryFiltersByAction(t *testing.T) {
	rec := newRecorder(t)
	ctx := context.Background()
	if _, err := rec.Record(ctx, Entry{Action: ActionConfigChange, Actor: "a"}); err != nil {
		t.Fatalf("rec: %v", err)
	}
	if _, err := rec.Record(ctx, Entry{Action: ActionTelemetryToggle, Actor: "a"}); err != nil {
		t.Fatalf("rec: %v", err)
	}
	got, err := rec.Query(ctx, Filter{Action: ActionTelemetryToggle})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 || got[0].Action != ActionTelemetryToggle {
		t.Errorf("filter wrong: %+v", got)
	}
}

func TestQueryFiltersByActor(t *testing.T) {
	rec := newRecorder(t)
	ctx := context.Background()
	_, _ = rec.Record(ctx, Entry{Action: ActionConfigChange, Actor: "alice"})
	_, _ = rec.Record(ctx, Entry{Action: ActionConfigChange, Actor: "bob"})
	got, _ := rec.Query(ctx, Filter{Actor: "bob"})
	if len(got) != 1 || got[0].Actor != "bob" {
		t.Errorf("actor filter: %+v", got)
	}
}

func TestQueryOrdersDescending(t *testing.T) {
	rec := newRecorder(t)
	ctx := context.Background()
	t1 := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 9, 11, 0, 0, 0, time.UTC)
	_, _ = rec.Record(ctx, Entry{Action: ActionConfigChange, Actor: "a", Timestamp: t1})
	_, _ = rec.Record(ctx, Entry{Action: ActionConfigChange, Actor: "a", Timestamp: t2})
	got, _ := rec.Query(ctx, Filter{})
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if !got[0].Timestamp.Equal(t2) {
		t.Errorf("ordering wrong: %v vs %v", got[0].Timestamp, got[1].Timestamp)
	}
}

func TestQueryTimeWindow(t *testing.T) {
	rec := newRecorder(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	for i, ts := range []time.Time{base, base.Add(time.Hour), base.Add(2 * time.Hour)} {
		_, err := rec.Record(ctx, Entry{
			Action: ActionConfigChange, Actor: "a", Timestamp: ts,
			Target: "t" + string(rune('0'+i)),
		})
		if err != nil {
			t.Fatalf("rec: %v", err)
		}
	}
	got, _ := rec.Query(ctx, Filter{
		Since: base.Add(30 * time.Minute), Until: base.Add(90 * time.Minute),
	})
	if len(got) != 1 || got[0].Target != "t1" {
		t.Errorf("window filter: %+v", got)
	}
}

func TestRecordRequiresActionAndActor(t *testing.T) {
	rec := newRecorder(t)
	ctx := context.Background()
	if _, err := rec.Record(ctx, Entry{Actor: "a"}); err == nil {
		t.Error("expected action error")
	}
	if _, err := rec.Record(ctx, Entry{Action: ActionConfigChange}); err == nil {
		t.Error("expected actor error")
	}
}

func TestNilRecorder(t *testing.T) {
	var r *Recorder
	if _, err := r.Record(context.Background(), Entry{Action: ActionConfigChange, Actor: "a"}); err == nil {
		t.Error("expected nil receiver error")
	}
}

func TestLimitCapsResults(t *testing.T) {
	rec := newRecorder(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = rec.Record(ctx, Entry{Action: ActionConfigChange, Actor: "a"})
	}
	got, _ := rec.Query(ctx, Filter{Limit: 2})
	if len(got) != 2 {
		t.Errorf("limit: got %d, want 2", len(got))
	}
}

func TestDetailsRoundTrip(t *testing.T) {
	rec := newRecorder(t)
	ctx := context.Background()
	in := map[string]any{
		"nested": map[string]any{
			"key": "value",
		},
		"count": float64(42),
	}
	_, err := rec.Record(ctx, Entry{
		Action: ActionRedactionUpdate, Actor: "ai", Details: in,
	})
	if err != nil {
		t.Fatalf("rec: %v", err)
	}
	got, _ := rec.Query(ctx, Filter{})
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	d := got[0].Details
	nested, ok := d["nested"].(map[string]any)
	if !ok || nested["key"] != "value" {
		t.Errorf("nested details lost: %+v", d)
	}
	if d["count"].(float64) != 42 {
		t.Errorf("count lost: %+v", d)
	}
}

package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
)

// newAuditRecorder is an audit.Recorder over a fresh store: the audit log's
// SQL is this package's, so its filters, ordering and JSON round trip are
// tested here.
func newAuditRecorder(t *testing.T) *audit.Recorder {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return audit.NewRecorder(store)
}

func TestAuditNilStoreIsNotInitialised(t *testing.T) {
	var store *Store
	rec := audit.NewRecorder(store)
	if _, err := rec.Record(context.Background(), audit.Entry{Action: audit.ActionConfigChange, Actor: "a"}); !errors.Is(err, audit.ErrNotInitialised) {
		t.Errorf("Record on a nil store: err = %v, want ErrNotInitialised", err)
	}
	if _, err := rec.Query(context.Background(), audit.Filter{}); !errors.Is(err, audit.ErrNotInitialised) {
		t.Errorf("Query on a nil store: err = %v, want ErrNotInitialised", err)
	}
}

func TestAuditRecordAndQueryRoundTrip(t *testing.T) {
	rec := newAuditRecorder(t)
	ctx := context.Background()
	got, err := rec.Record(ctx, audit.Entry{
		Action: audit.ActionConfigChange,
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
	entries, err := rec.Query(ctx, audit.Filter{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	e := entries[0]
	if e.Action != audit.ActionConfigChange || e.Actor != "felix@example" || e.Target != "config.yaml" {
		t.Errorf("entry mismatch: %+v", e)
	}
	if e.Details["path"] != "tls.enabled" {
		t.Errorf("details lost: %+v", e.Details)
	}
}

func TestAuditQueryFiltersByAction(t *testing.T) {
	rec := newAuditRecorder(t)
	ctx := context.Background()
	if _, err := rec.Record(ctx, audit.Entry{Action: audit.ActionConfigChange, Actor: "a"}); err != nil {
		t.Fatalf("rec: %v", err)
	}
	if _, err := rec.Record(ctx, audit.Entry{Action: audit.ActionTelemetryToggle, Actor: "a"}); err != nil {
		t.Fatalf("rec: %v", err)
	}
	got, err := rec.Query(ctx, audit.Filter{Action: audit.ActionTelemetryToggle})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 || got[0].Action != audit.ActionTelemetryToggle {
		t.Errorf("filter wrong: %+v", got)
	}
}

func TestAuditQueryFiltersByActor(t *testing.T) {
	rec := newAuditRecorder(t)
	ctx := context.Background()
	_, _ = rec.Record(ctx, audit.Entry{Action: audit.ActionConfigChange, Actor: "alice"})
	_, _ = rec.Record(ctx, audit.Entry{Action: audit.ActionConfigChange, Actor: "bob"})
	got, _ := rec.Query(ctx, audit.Filter{Actor: "bob"})
	if len(got) != 1 || got[0].Actor != "bob" {
		t.Errorf("actor filter: %+v", got)
	}
}

func TestAuditQueryOrdersDescending(t *testing.T) {
	rec := newAuditRecorder(t)
	ctx := context.Background()
	t1 := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 9, 11, 0, 0, 0, time.UTC)
	_, _ = rec.Record(ctx, audit.Entry{Action: audit.ActionConfigChange, Actor: "a", Timestamp: t1})
	_, _ = rec.Record(ctx, audit.Entry{Action: audit.ActionConfigChange, Actor: "a", Timestamp: t2})
	got, _ := rec.Query(ctx, audit.Filter{})
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if !got[0].Timestamp.Equal(t2) {
		t.Errorf("ordering wrong: %v vs %v", got[0].Timestamp, got[1].Timestamp)
	}
}

func TestAuditQueryTimeWindow(t *testing.T) {
	rec := newAuditRecorder(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	for i, ts := range []time.Time{base, base.Add(time.Hour), base.Add(2 * time.Hour)} {
		_, err := rec.Record(ctx, audit.Entry{
			Action: audit.ActionConfigChange, Actor: "a", Timestamp: ts,
			Target: "t" + string(rune('0'+i)),
		})
		if err != nil {
			t.Fatalf("rec: %v", err)
		}
	}
	got, _ := rec.Query(ctx, audit.Filter{
		Since: base.Add(30 * time.Minute), Until: base.Add(90 * time.Minute),
	})
	if len(got) != 1 || got[0].Target != "t1" {
		t.Errorf("window filter: %+v", got)
	}
}

func TestAuditLimitCapsResults(t *testing.T) {
	rec := newAuditRecorder(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = rec.Record(ctx, audit.Entry{Action: audit.ActionConfigChange, Actor: "a"})
	}
	got, _ := rec.Query(ctx, audit.Filter{Limit: 2})
	if len(got) != 2 {
		t.Errorf("limit: got %d, want 2", len(got))
	}
}

func TestAuditDetailsRoundTrip(t *testing.T) {
	rec := newAuditRecorder(t)
	ctx := context.Background()
	in := map[string]any{
		"nested": map[string]any{
			"key": "value",
		},
		"count": float64(42),
	}
	_, err := rec.Record(ctx, audit.Entry{
		Action: audit.ActionRedactionUpdate, Actor: "ai", Details: in,
	})
	if err != nil {
		t.Fatalf("rec: %v", err)
	}
	got, _ := rec.Query(ctx, audit.Filter{})
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

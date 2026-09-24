package domainmigration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/domainevents"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func writeRecords(t *testing.T, path string, records ...domainevents.Record) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(file)
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestImportRotatedHistoryIsOrderedIdempotentAndDeduplicatesBridge(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	path := filepath.Join(t.TempDir(), "domain-events.jsonl")
	base := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	old := domainevents.Record{Kind: domainevents.KindWorkflowStarted, At: base, Payload: json.RawMessage(`{"WorkflowID":"wf:1","At":"2026-09-24T08:00:00Z"}`)}
	bridged := domainevents.Record{Kind: domainevents.KindBudgetExceeded, At: base.Add(time.Minute), Payload: json.RawMessage(`{"BudgetID":"daily","SpentUSD":12,"LimitUSD":10,"At":"2026-09-24T08:01:00Z"}`)}
	current := domainevents.Record{Kind: domainevents.KindRuleCorpusReloaded, At: base.Add(2 * time.Minute), Payload: json.RawMessage(`{"SourceCount":3,"TotalTokens":300,"At":"2026-09-24T08:02:00Z"}`)}
	writeRecords(t, path+".2", old)
	writeRecords(t, path+".1", bridged)
	writeRecords(t, path, current)

	bridgeEnvelope, err := domainevents.EnvelopeFromRecord(bridged)
	if err != nil {
		t.Fatal(err)
	}
	bridgeEnvelope.ID = "live-bridge-id"
	bridgeEnvelope.Source = "domain_bus"
	if err := store.Append(ctx, bridgeEnvelope); err != nil {
		t.Fatal(err)
	}

	got, err := Import(ctx, store, path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Read != 3 || got.Imported != 2 || got.Duplicates != 1 || got.Skipped != 0 {
		t.Fatalf("first import = %+v", got)
	}
	got, err = Import(ctx, store, path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Read != 3 || got.Imported != 0 || got.Duplicates != 3 {
		t.Fatalf("second import = %+v", got)
	}
	events, err := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypeDomain, Limit: 10})
	if err != nil || len(events) != 3 {
		t.Fatalf("canonical domain events = %d, err = %v", len(events), err)
	}
	for i, want := range []string{domainevents.KindWorkflowStarted, domainevents.KindBudgetExceeded, domainevents.KindRuleCorpusReloaded} {
		payload, ok := events[i].Payload.(*eventschema.DomainEvent)
		if !ok || payload.Kind != want {
			t.Fatalf("event[%d] = %#v, want kind %q", i, events[i].Payload, want)
		}
	}
}

func TestEnvelopeFromRecordIDIsStableAndPayloadIsCompacted(t *testing.T) {
	rec := domainevents.Record{
		Kind:    domainevents.KindBudgetExceeded,
		At:      time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC),
		Payload: json.RawMessage(`{ "limit": 10, "budget": "daily" }`),
	}
	one, err := domainevents.EnvelopeFromRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	two, err := domainevents.EnvelopeFromRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != two.ID || len(one.ID) > 64 {
		t.Fatalf("stable IDs = %q and %q", one.ID, two.ID)
	}
	if got := string(one.Payload.(*eventschema.DomainEvent).Data); got != `{"limit":10,"budget":"daily"}` {
		t.Fatalf("payload = %s", got)
	}
}

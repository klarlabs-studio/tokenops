package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestEventsQueriesCanonicalStoreWithTimeBounds(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "events.db")
	store, err := sqlite.Open(ctx, dbPath, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	for i, event := range []struct {
		kind string
		at   time.Time
	}{
		{"workflow.started", base},
		{"budget.exceeded", base.Add(time.Minute)},
		{"workflow.started", base.Add(2 * time.Minute)},
	} {
		if err := store.Append(ctx, &eventschema.Envelope{
			ID: "event-" + string(rune('1'+i)), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypeDomain, Timestamp: event.at, Source: "test",
			Payload: &eventschema.DomainEvent{Kind: event.kind, Data: json.RawMessage(`{}`)},
		}); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newEventsCmd(nil)
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--db", dbPath, "--since", base.Format(time.RFC3339), "--until", base.Add(time.Minute).Format(time.RFC3339), "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Counts map[string]int64 `json:"counts"`
		Total  int64            `json:"total"`
		Source string           `json:"source"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || got.Counts["workflow.started"] != 1 || got.Counts["budget.exceeded"] != 1 || got.Source != "sqlite" {
		t.Fatalf("events output = %+v", got)
	}
}

func TestEventsTextOutputSortsKinds(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "events.db")
	store, err := sqlite.Open(ctx, dbPath, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{"zeta.kind", "alpha.kind"} {
		if err := store.Append(ctx, &eventschema.Envelope{
			ID: "event-" + string(rune('1'+i)), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypeDomain, Timestamp: time.Now().UTC(), Source: "test",
			Payload: &eventschema.DomainEvent{Kind: kind, Data: json.RawMessage(`{}`)},
		}); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := newEventsCmd(nil)
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--db", dbPath})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Index(text, "alpha.kind") < strings.Index(text, "zeta.kind") {
		return
	}
	t.Fatalf("kinds are not sorted in output:\n%s", text)
}

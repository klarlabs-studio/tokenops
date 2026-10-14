package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Hooks run on every turn and must not add write-lock pressure to the store
// every tokenops process shares: a read-only handle runs no migration and
// cannot write.
func TestOpenReadOnlyCannotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	rw, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = rw.Close()
	ro, err := OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = ro.Close() }()
	env := mustPromptEnvelope(t, "ro-1", time.Now(), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})
	if err := ro.Append(context.Background(), env); err == nil {
		t.Fatal("read-only store accepted a write")
	}
}

func TestOpenReadOnlyMissingStore(t *testing.T) {
	if _, err := OpenReadOnly(context.Background(), filepath.Join(t.TempDir(), "absent.db")); err == nil {
		t.Fatal("opened a store that does not exist")
	}
}

func TestLatestAttributesBySource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	add := func(id, source string, at time.Time, attrs map[string]string) {
		env := mustPromptEnvelope(t, id, at, &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})
		env.Source, env.Attributes = source, attrs
		if err := s.Append(ctx, env); err != nil {
			t.Fatal(err)
		}
	}
	add("old", "claude-usage-meter", now.Add(-10*time.Minute), map[string]string{"five_hour_used_pct": "10.00"})
	add("new", "claude-usage-meter", now.Add(-2*time.Minute), map[string]string{"five_hour_used_pct": "40.00"})
	add("other", "claude-code-jsonl", now.Add(-1*time.Minute), map[string]string{"five_hour_used_pct": "99.00"})
	add("nokey", "claude-usage-meter", now.Add(-30*time.Second), map[string]string{"org_id": "x"})

	attrs, at, ok, err := s.LatestAttributesBySource(ctx, "claude-usage-meter", "five_hour_used_pct", now.Add(-time.Hour))
	if err != nil || !ok {
		t.Fatalf("LatestAttributesBySource = %v, %v", ok, err)
	}
	if attrs["five_hour_used_pct"] != "40.00" || at.Sub(now.Add(-2*time.Minute)).Abs() > time.Second {
		t.Errorf("got %v at %v, want the newest reading carrying the key from that source", attrs, at)
	}
	if _, _, ok, _ := s.LatestAttributesBySource(ctx, "claude-usage-meter", "five_hour_used_pct", now.Add(-time.Minute)); ok {
		t.Error("a reading older than since was returned; a stale quota must not read as current")
	}
}

// A burn-rate look-back needs the reading as it stood a while ago: the
// newest one at or before a moment, not older than a bound.
func TestAttributesBySourceBetween(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	add := func(id string, at time.Time, used string) {
		env := mustPromptEnvelope(t, id, at, &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})
		env.Source, env.Attributes = "claude-usage-meter", map[string]string{"five_hour_used_pct": used}
		if err := s.Append(ctx, env); err != nil {
			t.Fatal(err)
		}
	}
	add("h2", now.Add(-2*time.Hour), "5.00")
	add("h1", now.Add(-65*time.Minute), "20.00")
	add("m10", now.Add(-10*time.Minute), "50.00")
	add("m1", now.Add(-time.Minute), "60.00")

	attrs, at, ok, err := s.AttributesBySourceBetween(ctx, "claude-usage-meter", "five_hour_used_pct", now.Add(-2*time.Hour+time.Minute), now.Add(-time.Hour))
	if err != nil || !ok || attrs["five_hour_used_pct"] != "20.00" || at.Sub(now.Add(-65*time.Minute)).Abs() > time.Second {
		t.Fatalf("got %v at %v (%v, %v); want the reading 65 minutes back", attrs, at, ok, err)
	}
	if _, _, ok, _ := s.AttributesBySourceBetween(ctx, "claude-usage-meter", "five_hour_used_pct", now.Add(-50*time.Minute), now.Add(-20*time.Minute)); ok {
		t.Error("found a reading where there is none")
	}
}

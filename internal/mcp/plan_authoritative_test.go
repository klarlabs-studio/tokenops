package mcp

import (
	"context"
	"strconv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type fakeReader struct{ events []*eventschema.Envelope }

func (f fakeReader) ReadEvents(_ context.Context, _ eventschema.EventType, _ time.Time) ([]*eventschema.Envelope, error) {
	return f.events, nil
}

func env(ts time.Time, attrs map[string]string) *eventschema.Envelope {
	return &eventschema.Envelope{Type: eventschema.EventTypePrompt, Timestamp: ts, Attributes: attrs}
}

func TestLatestAuthoritativeWindow_CodexPrimary(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	reset := now.Add(90 * time.Minute).Unix()
	reader := fakeReader{events: []*eventschema.Envelope{
		env(now.Add(-20*time.Minute), map[string]string{"primary_used_pct": "40.00"}),
		// newest snapshot wins:
		env(now.Add(-2*time.Minute), map[string]string{
			"primary_used_pct":  "83.50",
			"primary_resets_at": strconv.FormatInt(reset, 10),
		}),
		env(now.Add(-1*time.Minute), map[string]string{"other": "x"}), // no key
	}}
	p, _ := plans.Lookup("codex-plus") // openai, 5h window
	a := latestAuthoritativeWindow(context.Background(), reader, eventschema.ProviderOpenAI, p, now)
	if a == nil {
		t.Fatal("expected an authoritative window from codex primary snapshot")
	}
	if a.UsedPct != 83.5 {
		t.Errorf("used_pct=%v want 83.5 (newest)", a.UsedPct)
	}
	if a.ResetsIn.Round(time.Minute) != 90*time.Minute {
		t.Errorf("resets_in=%v want ~90m", a.ResetsIn)
	}
	if a.Source != "codex:primary" {
		t.Errorf("source=%q", a.Source)
	}
}

func TestLatestAuthoritativeMonthly_CopilotRemainingInverts(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	reader := fakeReader{events: []*eventschema.Envelope{
		env(now.Add(-time.Minute), map[string]string{
			"percent_remaining": "12.00", "quota_reset_date": "2026-08-01",
		}),
	}}
	// Copilot has no rolling window — it's a MONTHLY meter.
	a := latestAuthoritativeMonthly(context.Background(), reader, eventschema.ProviderGitHub, now)
	if a == nil {
		t.Fatal("expected a monthly authoritative reading from copilot quota")
	}
	// remaining 12% => used 88%.
	if a.UsedPct != 88 {
		t.Errorf("used_pct=%v want 88 (100-remaining)", a.UsedPct)
	}
	if a.Source != "copilot:monthly" {
		t.Errorf("source=%q want copilot:monthly", a.Source)
	}
}

func TestLatestAuthoritativeMonthly_CursorUsedPct(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	reader := fakeReader{events: []*eventschema.Envelope{
		env(now.Add(-time.Minute), map[string]string{"used_pct": "63.00"}),
	}}
	a := latestAuthoritativeMonthly(context.Background(), reader, eventschema.ProviderCursor, now)
	if a == nil || a.UsedPct != 63 {
		t.Fatalf("cursor monthly used_pct: got %+v want 63", a)
	}
}

// A provider with no monthly meter (e.g. anthropic) yields nil.
func TestLatestAuthoritativeMonthly_NoneForWindowProviders(t *testing.T) {
	now := time.Unix(3_000_000, 0).UTC()
	reader := fakeReader{events: []*eventschema.Envelope{
		env(now, map[string]string{"five_hour_used_pct": "50.00"}),
	}}
	if a := latestAuthoritativeMonthly(context.Background(), reader, eventschema.ProviderAnthropic, now); a != nil {
		t.Errorf("anthropic has no monthly meter; want nil, got %+v", a)
	}
}

func TestLatestAuthoritativeWindow_NoSnapshotReturnsNil(t *testing.T) {
	now := time.Unix(3_000_000, 0).UTC()
	reader := fakeReader{events: []*eventschema.Envelope{
		env(now, map[string]string{"granularity": "assistant_turn"}), // no quota attr
	}}
	p, _ := plans.Lookup("claude-max-20x")
	if a := latestAuthoritativeWindow(context.Background(), reader, eventschema.ProviderAnthropic, p, now); a != nil {
		t.Errorf("expected nil when no snapshot present, got %+v", a)
	}
}

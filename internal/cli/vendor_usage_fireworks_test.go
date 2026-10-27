package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type fireworksEvents []*eventschema.Envelope

func (f fireworksEvents) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	return f, nil
}

func fireworksReading(at time.Time, attrs map[string]string) *eventschema.Envelope {
	base := map[string]string{"billing": "per_token", "granularity": "quota_snapshot", "account_id": "acme-test",
		"extra_usage_currency": "USD", "extra_usage_used": "900.00", "extra_usage_limit": "4000.00"}
	for k, v := range attrs {
		base[k] = v
	}
	return &eventschema.Envelope{Type: eventschema.EventTypePrompt, Timestamp: at, Source: "fireworks-usage",
		Attributes: base, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderFireworks}}
}

// `vendor-usage status` names why Fireworks' figure is account-wide, and
// a block, instead of leaving the fallback silent.
func TestVendorUsageStatusNamesTheFireworksFallback(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	reason := "your user wasn't found — set it with `tokenops vendor-usage enable fireworks --user-id <id>`"
	note := fireworksStatusNote(context.Background(), fireworksEvents{
		fireworksReading(now.Add(-time.Minute), map[string]string{"scope": "account", "scope_reason": reason}),
	}, now)
	if note != "account-wide: "+reason {
		t.Errorf("note %q", note)
	}
	until := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	note = fireworksStatusNote(context.Background(), fireworksEvents{
		fireworksReading(now.Add(-time.Minute), map[string]string{"scope": "user", "extra_usage_limit_reached": "true",
			"extra_usage_used": "105.34", "extra_usage_limit": "100.00", "extra_usage_blocked_until": until.Format(time.RFC3339)}),
	}, now)
	if !strings.HasPrefix(note, "blocked until "+until.Local().Format("2 Jan")) {
		t.Errorf("note %q", note)
	}
	if note := fireworksStatusNote(context.Background(), fireworksEvents{
		fireworksReading(now.Add(-time.Minute), map[string]string{"scope": "user"}),
	}, now); note != "" {
		t.Errorf("the operator's own reading has a note: %q", note)
	}
}

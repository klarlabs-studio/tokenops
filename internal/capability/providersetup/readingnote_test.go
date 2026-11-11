package providersetup

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type readings []*eventschema.Envelope

func (r readings) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	return r, nil
}

// The note says a block from a reading its reader still stands behind. A
// reading the reader stopped replacing says what was true then: the limit
// may have been raised since, so no block is announced from it.
func TestReadingNote(t *testing.T) {
	read := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	until := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	reading := func(extra map[string]string) readings {
		attrs := map[string]string{
			"extra_usage_used": "100.00", "extra_usage_limit": "100.00", "extra_usage_currency": "USD",
			heartbeat.Attr: "55",
		}
		for k, v := range extra {
			attrs[k] = v
		}
		return readings{{
			Type: eventschema.EventTypePrompt, Source: "fireworks-usage", Timestamp: read, Attributes: attrs,
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderFireworks},
		}}
	}
	blocked := map[string]string{"extra_usage_limit_reached": "true", "extra_usage_blocked_until": until.Format(time.RFC3339)}
	wide := map[string]string{"scope": "account", "scope_reason": "your user wasn't found"}
	both := map[string]string{}
	for _, m := range []map[string]string{blocked, wide} {
		for k, v := range m {
			both[k] = v
		}
	}
	working, stopped := read.Add(30*time.Minute), read.Add(26*time.Hour)
	block := "blocked until " + until.Local().Format("2 Jan") + " by your spend limit"
	for name, tc := range map[string]struct {
		r    readings
		now  time.Time
		want string
	}{
		"no reading":                   {readings{}, working, ""},
		"neither":                      {reading(nil), working, ""},
		"blocked":                      {reading(blocked), working, block},
		"blocked, the reader stopped":  {reading(blocked), stopped, ""},
		"account-wide":                 {reading(wide), working, "account-wide: your user wasn't found"},
		"account-wide, reader stopped": {reading(wide), stopped, "account-wide: your user wasn't found"},
		"blocked and account-wide":     {reading(both), working, block},
		"both, and the reader stopped": {reading(both), stopped, "account-wide: your user wasn't found"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ReadingNote(context.Background(), tc.r, eventschema.ProviderFireworks, tc.now); got != tc.want {
				t.Errorf("note %q, want %q", got, tc.want)
			}
		})
	}
}

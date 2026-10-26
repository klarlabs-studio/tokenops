package plans

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// An unstarted Codex week, as the app server reports it on consecutive
// polls: nothing used, and resets_at moving with each poll to a full week
// from that poll.
func unstartedPoll(at time.Time) *eventschema.Envelope {
	return codexReading(at, 0, at.Add(weekDuration))
}

func TestVendorWindowsMarksAnUnstartedWindow(t *testing.T) {
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	reader := authFakeReader{events: []*eventschema.Envelope{
		unstartedPoll(now.Add(-30 * time.Minute)),
		unstartedPoll(now.Add(-15 * time.Minute)),
	}}
	got := VendorWindows(context.Background(), reader, eventschema.ProviderOpenAI, now)
	if len(got) != 1 {
		t.Fatalf("windows %+v", got)
	}
	w := got[0]
	if !w.NotStarted {
		t.Errorf("an unstarted week is not marked: %+v", w)
	}
	if !w.ResetsAt.IsZero() || w.ResetsIn != "" {
		t.Errorf("an unstarted week still counts down: resets_at %v, resets_in %q", w.ResetsAt, w.ResetsIn)
	}
	if w.Pace != nil {
		t.Errorf("an unstarted week has a pace: %+v", w.Pace)
	}
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["not_started"] != true {
		t.Errorf("the API lacks not_started: %s", raw)
	}
	if _, ok := wire["resets_in"]; ok {
		t.Errorf("the API still carries a countdown: %s", raw)
	}
}

func TestNotStartedNeedsTwoConsecutiveReadings(t *testing.T) {
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		envs []*eventschema.Envelope
		want bool
	}{
		{
			name: "one reading cannot show the window drifts",
			envs: []*eventschema.Envelope{unstartedPoll(now.Add(-15 * time.Minute))},
		},
		{
			name: "a started window with a fixed reset is counted down",
			envs: []*eventschema.Envelope{
				codexReading(now.Add(-30*time.Minute), 0, now.Add(3*24*time.Hour)),
				codexReading(now.Add(-15*time.Minute), 0, now.Add(3*24*time.Hour)),
			},
		},
		{
			name: "used windows always have a countdown",
			envs: []*eventschema.Envelope{
				codexReading(now.Add(-30*time.Minute), 4, now.Add(weekDuration-30*time.Minute)),
				codexReading(now.Add(-15*time.Minute), 4, now.Add(weekDuration-15*time.Minute)),
			},
		},
		{
			name: "a window that began a day ago is not unstarted",
			envs: []*eventschema.Envelope{
				codexReading(now.Add(-30*time.Minute), 0, now.Add(6*24*time.Hour)),
				codexReading(now.Add(-15*time.Minute), 0, now.Add(6*24*time.Hour)),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := authFakeReader{events: tc.envs}
			got := VendorWindows(context.Background(), r, eventschema.ProviderOpenAI, now)
			if len(got) != 1 || got[0].NotStarted != tc.want {
				t.Errorf("windows %+v, want not_started %v", got, tc.want)
			}
		})
	}
}

func TestNotStartedReadsTheReadingTimeNotNow(t *testing.T) {
	// A reading an hour old, still unstarted: its reset is a full week from
	// the reading, not from now.
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	r := authFakeReader{events: []*eventschema.Envelope{
		unstartedPoll(now.Add(-75 * time.Minute)),
		unstartedPoll(now.Add(-60 * time.Minute)),
	}}
	got := VendorWindows(context.Background(), r, eventschema.ProviderOpenAI, now)
	if len(got) != 1 || !got[0].NotStarted {
		t.Errorf("windows %+v", got)
	}
}

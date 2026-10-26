package plans

import (
	"strconv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A weekly Codex window as the app server reports it: 62% used at 09:00,
// due to reset at 18:00 on the 30th. The next reading, an hour later, shows
// the window reset and a new one begun.
var (
	earlyPrevAt    = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	earlyOldReset  = time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)
	earlyNewAt     = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	earlyNewReset  = time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)
	weekDuration   = 7 * 24 * time.Hour
	fiveHourLength = 5 * time.Hour
)

func weekly(name string, used float64, at, resets time.Time, d time.Duration) VendorWindow {
	return VendorWindow{Name: name, UsedPct: used, ObservedAt: at, ResetsAt: resets, Duration: d}
}

func TestDetectEarlyReset(t *testing.T) {
	prev := weekly("week", 62, earlyPrevAt, earlyOldReset, weekDuration)
	cases := []struct {
		name        string
		prev, cur   VendorWindow
		planChanged bool
		want        bool
	}{
		{
			name: "window reset five days early",
			prev: prev,
			cur:  weekly("week", 0, earlyNewAt, earlyNewReset, weekDuration),
			want: true,
		},
		{
			name: "normal reset at the reported time",
			prev: weekly("week", 70, earlyOldReset.Add(-time.Hour), earlyOldReset, weekDuration),
			cur:  weekly("week", 0, earlyOldReset.Add(10*time.Minute), earlyOldReset.Add(7*24*time.Hour+10*time.Minute), weekDuration),
		},
		{
			name: "reset within the hour of its reported time is not early",
			prev: weekly("week", 70, earlyOldReset.Add(-2*time.Hour), earlyOldReset, weekDuration),
			cur:  weekly("week", 0, earlyOldReset.Add(-30*time.Minute), earlyOldReset.Add(7*24*time.Hour-30*time.Minute), weekDuration),
		},
		{
			name: "little used before the reset",
			prev: weekly("week", 15, earlyPrevAt, earlyOldReset, weekDuration),
			cur:  weekly("week", 0, earlyNewAt, earlyNewReset, weekDuration),
		},
		{
			name: "still well used after the reset: a usage drop, not a reset",
			prev: prev,
			cur:  weekly("week", 12, earlyNewAt, earlyNewReset, weekDuration),
		},
		{
			name:        "plan changed between the readings",
			prev:        prev,
			cur:         weekly("week", 0, earlyNewAt, earlyNewReset, weekDuration),
			planChanged: true,
		},
		{
			name: "window length changed",
			prev: prev,
			cur:  weekly("week", 0, earlyNewAt, earlyNewReset.Add(-weekDuration+fiveHourLength), fiveHourLength),
		},
		{
			name: "a different window",
			prev: prev,
			cur:  weekly("5h", 0, earlyNewAt, earlyNewReset, weekDuration),
		},
		{
			name: "reset did not move later",
			prev: prev,
			cur:  weekly("week", 0, earlyNewAt, earlyOldReset, weekDuration),
		},
		{
			name: "reading before the previous one",
			prev: prev,
			cur:  weekly("week", 0, earlyPrevAt.Add(-time.Minute), earlyNewReset, weekDuration),
		},
		{
			name: "no reset time on the new reading",
			prev: prev,
			cur:  weekly("week", 0, earlyNewAt, time.Time{}, weekDuration),
		},
		{
			name: "new window longer than its length",
			prev: prev,
			cur:  weekly("week", 0, earlyNewAt, earlyNewAt.Add(3*weekDuration), weekDuration),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DetectEarlyReset(tc.prev, tc.cur, tc.planChanged)
			if ok != tc.want {
				t.Fatalf("detected %v, want %v (%+v)", ok, tc.want, got)
			}
		})
	}
}

func TestDetectEarlyResetFields(t *testing.T) {
	got, ok := DetectEarlyReset(weekly("week", 62, earlyPrevAt, earlyOldReset, weekDuration),
		weekly("week", 0, earlyNewAt, earlyNewReset, weekDuration), false)
	if !ok {
		t.Fatal("not detected")
	}
	if got.PrevUsedPct != 62 || !got.PrevResetsAt.Equal(earlyOldReset) || !got.PrevObservedAt.Equal(earlyPrevAt) ||
		!got.ObservedAt.Equal(earlyNewAt) || !got.ResetsAt.Equal(earlyNewReset) || got.Name != "week" {
		t.Errorf("fields %+v", got)
	}
	// The reset came after the previous reading and no sooner than the
	// new one, so at least this early.
	if want := 5*24*time.Hour + 8*time.Hour; got.Earliness() != want {
		t.Errorf("earliness %v, want at least %v", got.Earliness(), want)
	}
}

// readings builds Codex app-server snapshots of the primary window.
func codexReading(at time.Time, used float64, resets time.Time) *eventschema.Envelope {
	return &eventschema.Envelope{Source: "codex-app-server", Timestamp: at, Attributes: map[string]string{
		"plan_type":          "plus",
		"primary_used_pct":   strconv.FormatFloat(used, 'f', 2, 64),
		"primary_window_min": "10080",
		"primary_resets_at":  strconv.FormatInt(resets.Unix(), 10),
	}}
}

func TestEarlyResetsInReadings(t *testing.T) {
	now := earlyNewAt.Add(2 * time.Hour)
	planCheck := func(from, to time.Time) bool { return false }
	cases := []struct {
		name string
		envs []*eventschema.Envelope
		want int
	}{
		{
			name: "one early reset in the readings",
			envs: []*eventschema.Envelope{
				codexReading(earlyPrevAt.Add(-2*time.Hour), 40, earlyOldReset),
				codexReading(earlyPrevAt, 62, earlyOldReset),
				codexReading(earlyNewAt, 0, earlyNewReset),
				codexReading(earlyNewAt.Add(time.Hour), 1, earlyNewReset),
			},
			want: 1,
		},
		{
			name: "readings across a normal reset",
			envs: []*eventschema.Envelope{
				codexReading(earlyOldReset.Add(-3*time.Hour), 70, earlyOldReset),
				codexReading(earlyOldReset.Add(10*time.Minute), 0, earlyOldReset.Add(weekDuration+10*time.Minute)),
			},
		},
		{
			name: "a gap of days with no readings is not read as a reset",
			envs: []*eventschema.Envelope{
				codexReading(earlyOldReset.Add(-6*24*time.Hour), 62, earlyOldReset),
				codexReading(earlyOldReset.Add(3*24*time.Hour), 2, earlyOldReset.Add(4*24*time.Hour)),
			},
		},
		{
			name: "a plan change between readings",
			envs: []*eventschema.Envelope{
				codexReading(earlyPrevAt, 62, earlyOldReset),
				{Source: "codex-app-server", Timestamp: earlyNewAt, Attributes: map[string]string{
					"plan_type": "pro", "primary_used_pct": "0.00", "primary_window_min": "10080",
					"primary_resets_at": strconv.FormatInt(earlyNewReset.Unix(), 10),
				}},
			},
		},
		{
			name: "a reset older than the visibility window",
			envs: []*eventschema.Envelope{
				codexReading(earlyPrevAt, 62, earlyOldReset),
				codexReading(earlyNewAt, 0, earlyNewReset),
			},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			since := now.Add(-EarlyResetVisibleFor)
			if tc.name == "a reset older than the visibility window" {
				since = earlyNewAt.Add(time.Hour)
			}
			got := EarlyResetsIn(tc.envs, eventschema.ProviderOpenAI, since, now, planCheck)
			if len(got) != tc.want {
				t.Fatalf("got %d early resets, want %d: %+v", len(got), tc.want, got)
			}
		})
	}
}

func TestEarlyResetsInSeesPlanChangeFromHistory(t *testing.T) {
	envs := []*eventschema.Envelope{
		codexReading(earlyPrevAt, 62, earlyOldReset),
		codexReading(earlyNewAt, 0, earlyNewReset),
	}
	now := earlyNewAt.Add(time.Hour)
	changed := func(from, to time.Time) bool { return from.Before(earlyNewAt) && !to.Before(earlyNewAt) }
	if got := EarlyResetsIn(envs, eventschema.ProviderOpenAI, now.Add(-time.Hour*24), now, changed); len(got) != 0 {
		t.Errorf("a switch in the period read as a reset: %+v", got)
	}
}

func TestHistoryChangedBetween(t *testing.T) {
	h := History{{Provider: "openai", Plan: "plus", From: earlyPrevAt}, {Provider: "anthropic", Plan: "max-20x", From: earlyNewAt}}
	if !h.ChangedBetween("openai", earlyPrevAt.Add(-time.Hour), earlyPrevAt.Add(time.Hour)) {
		t.Error("a change inside the span is missed")
	}
	if h.ChangedBetween("openai", earlyPrevAt, earlyNewAt) {
		t.Error("a change at the start of the span counts as inside it")
	}
	if h.ChangedBetween("openai", earlyNewAt, earlyNewAt.Add(time.Hour)) {
		t.Error("another provider's change counts")
	}
}

package heartbeat

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// A reading is stored when it differs from the last one stored, and again
// once Every has passed since: never in between.
func TestKeeperDue(t *testing.T) {
	var k Keeper
	if !k.Due("a", t0) {
		t.Fatal("the first reading is not due")
	}
	k.Stored("a", t0)
	for _, tc := range []struct {
		name  string
		key   string
		after time.Duration
		want  bool
	}{
		{"unchanged, at once", "a", 0, false},
		{"unchanged, a poll later", "a", 5 * time.Minute, false},
		{"unchanged, just short of the heartbeat", "a", Every - time.Second, false},
		{"unchanged, at the heartbeat", "a", Every, true},
		{"unchanged, long after", "a", 6 * time.Hour, true},
		{"changed, at once", "b", 0, true},
		{"changed, a poll later", "b", 5 * time.Minute, true},
		{"unchanged, the clock set back", "a", -time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := k.Due(tc.key, t0.Add(tc.after)); got != tc.want {
				t.Errorf("Due = %v, want %v", got, tc.want)
			}
		})
	}
}

// A reading that was due and could not be stored stays due: only Stored
// moves the keeper on.
func TestKeeperStaysDueUntilStored(t *testing.T) {
	var k Keeper
	k.Stored("a", t0)
	later := t0.Add(time.Minute)
	if !k.Due("b", later) || !k.Due("b", later.Add(time.Minute)) {
		t.Fatal("a changed reading that was never stored is no longer due")
	}
	k.Stored("b", later)
	if k.Due("b", later.Add(time.Minute)) {
		t.Error("the stored reading is still due a minute later")
	}
	if !k.Due("a", later.Add(time.Minute)) {
		t.Error("the reading before last is not due: it differs from the last one stored")
	}
}

// A reading stands for the longest two stored readings can be apart while
// its reader works, and two polls missed on top.
func TestGoodFor(t *testing.T) {
	for _, tc := range []struct {
		interval time.Duration
		want     time.Duration
	}{
		{2 * time.Minute, 16 * time.Minute},
		{5 * time.Minute, 25 * time.Minute},
		{15 * time.Minute, 55 * time.Minute},
		{8 * time.Hour, 24*time.Hour + 10*time.Minute},
		{0, Every},
		{-time.Minute, Every},
	} {
		if got := GoodFor(tc.interval); got != tc.want {
			t.Errorf("GoodFor(%s) = %s, want %s", tc.interval, got, tc.want)
		}
	}
}

// A reader that works stores often enough for its reading never to be
// read as stale: whatever the poll interval, readings are stored closer
// together than a reading stands for.
func TestAWorkingReaderIsNeverStale(t *testing.T) {
	for _, interval := range []time.Duration{
		time.Minute, 2 * time.Minute, 5 * time.Minute, 7 * time.Minute, 10 * time.Minute,
		15 * time.Minute, time.Hour, 8 * time.Hour,
	} {
		var k Keeper
		last := t0
		k.Stored("same", t0)
		var widest time.Duration
		// An idle account polled for two days, each poll a second late.
		for now := t0.Add(interval); now.Before(t0.Add(48 * time.Hour)); now = now.Add(interval + time.Second) {
			if !k.Due("same", now) {
				continue
			}
			widest = max(widest, now.Sub(last))
			k.Stored("same", now)
			last = now
		}
		if widest == 0 {
			t.Errorf("interval %s: nothing was stored again", interval)
		}
		if limit := GoodFor(interval); widest >= limit {
			t.Errorf("interval %s: stored readings were %s apart, and a reading stands for %s", interval, widest, limit)
		}
	}
}

func TestStampAndFreshFor(t *testing.T) {
	attrs := map[string]string{"five_hour_used_pct": "40.00"}
	Stamp(attrs, 5*time.Minute)
	if got := attrs[Attr]; got != "25" {
		t.Errorf("%s = %q, want 25", Attr, got)
	}
	if d, ok := FreshFor(attrs); !ok || d != 25*time.Minute {
		t.Errorf("FreshFor = %s, %v; want 25m, true", d, ok)
	}

	// Part of a minute is rounded up: a reading is never said to stand
	// for less than its reader keeps it.
	Stamp(attrs, 90*time.Second)
	if got := attrs[Attr]; got != "15" {
		t.Errorf("%s = %q for a 90s interval, want 15 (14m30s rounded up)", Attr, got)
	}

	for name, attrs := range map[string]map[string]string{
		"no attributes":   nil,
		"not stamped":     {"five_hour_used_pct": "40.00"},
		"empty":           {Attr: ""},
		"not a number":    {Attr: "soon"},
		"zero":            {Attr: "0"},
		"negative":        {Attr: "-5"},
		"not whole":       {Attr: "2.5"},
		"with whitespace": {Attr: " 25"},
	} {
		if d, ok := FreshFor(attrs); ok {
			t.Errorf("%s: FreshFor = %s, true; want no answer", name, d)
		}
	}
}

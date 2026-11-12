package claudelimits

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", fileName)
	used, limit := 314.12, 500.0
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	in := Reading{
		ObservedAt: at,
		FiveHour:   &Window{UsedPct: 23.5, ResetsAt: 1738425600},
		SpendLimit: &Window{UsedPct: 62.8, ResetsAt: 1740787200, UsedUSD: &used, LimitUSD: &limit, Period: "monthly"},
	}
	if err := Write(path, in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Read(path)
	if err != nil || !ok {
		t.Fatalf("Read = ok %v, err %v", ok, err)
	}
	if !got.ObservedAt.Equal(at) || got.FiveHour.UsedPct != 23.5 || got.SevenDay != nil {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if *got.SpendLimit.UsedUSD != used || *got.SpendLimit.LimitUSD != limit || got.SpendLimit.Period != "monthly" {
		t.Fatalf("spend limit lost: %+v", got.SpendLimit)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestEmptyReadingIsNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), fileName)
	if err := Write(path, Reading{ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an empty reading was written: %v", err)
	}
	if _, ok, err := Read(path); ok || err != nil {
		t.Fatalf("Read of a missing file = ok %v, err %v", ok, err)
	}
}

// A status line killed between creating its temporary file and renaming
// it leaves the file behind; the next write clears it, but never a
// temporary file young enough to be another writer's.
func TestWriteClearsStaleTemporaryFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir := filepath.Join(home, ".tokenops")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, fileName+".1234567")
	live := filepath.Join(dir, fileName+".7654321")
	for _, p := range []string{stale, live} {
		if err := os.WriteFile(p, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	r := Reading{ObservedAt: time.Now(), FiveHour: &Window{UsedPct: 10, ResetsAt: 1}}
	if err := Write("", r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale temporary file still there: %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("a live writer's temporary file was removed: %v", err)
	}
	if _, ok, err := Read(""); err != nil || !ok {
		t.Fatalf("Read = ok %v, err %v", ok, err)
	}
}

// Each write keeps the readings before it, a minute or more apart and no
// older than KeepFor, so a hook can read the last hour's rate from this
// one file (the run-out forecast) without the event store.
func TestWriteKeepsEarlierReadings(t *testing.T) {
	path := filepath.Join(t.TempDir(), fileName)
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	// One reading every 30 seconds for three hours.
	for i := 0; i <= 3*120; i++ {
		at := start.Add(time.Duration(i) * 30 * time.Second)
		if err := Write(path, Reading{ObservedAt: at, FiveHour: &Window{UsedPct: float64(i) / 10, ResetsAt: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := Read(path)
	if err != nil || !ok {
		t.Fatalf("Read = %v, %v", ok, err)
	}
	newest := start.Add(3 * time.Hour)
	if !got.ObservedAt.Equal(newest) {
		t.Fatalf("newest %v", got.ObservedAt)
	}
	if len(got.Earlier) == 0 || len(got.Earlier) > int(KeepFor/time.Minute)+1 {
		t.Fatalf("kept %d earlier readings", len(got.Earlier))
	}
	for i, e := range got.Earlier {
		if len(e.Earlier) != 0 || newest.Sub(e.ObservedAt) > KeepFor || !e.ObservedAt.Before(newest) {
			t.Fatalf("earlier[%d] = %v (nested %d)", i, e.ObservedAt, len(e.Earlier))
		}
		if i > 0 && e.ObservedAt.Sub(got.Earlier[i-1].ObservedAt) < time.Minute {
			t.Fatalf("earlier[%d] %v within a minute of %v", i, e.ObservedAt, got.Earlier[i-1].ObservedAt)
		}
	}
	// The newest reading in a span, the current one included.
	if r, ok := got.Between(newest.Add(-2*time.Hour), newest.Add(-time.Hour)); !ok ||
		r.ObservedAt.After(newest.Add(-time.Hour)) || r.ObservedAt.Before(newest.Add(-time.Hour-time.Minute)) {
		t.Errorf("Between an hour ago = %v, %v", r.ObservedAt, ok)
	}
	if r, ok := got.Between(newest.Add(-time.Minute), newest); !ok || !r.ObservedAt.Equal(newest) {
		t.Errorf("Between now = %v, %v", r.ObservedAt, ok)
	}
	if _, ok := got.Between(start.Add(-time.Hour), start); ok {
		t.Errorf("a reading older than kept was found")
	}
}

// A reading older than the stored one replaces it, and keeps as earlier
// only the readings that came before it.
func TestAnOlderWriteKeepsOnlyWhatCameBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), fileName)
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	for _, r := range []Reading{
		{ObservedAt: at, FiveHour: &Window{UsedPct: 10, ResetsAt: 1}},
		{ObservedAt: at.Add(2 * time.Minute), FiveHour: &Window{UsedPct: 12, ResetsAt: 1}},
		{ObservedAt: at.Add(time.Minute), FiveHour: &Window{UsedPct: 11, ResetsAt: 1}},
	} {
		if err := Write(path, r); err != nil {
			t.Fatal(err)
		}
	}
	got, _, _ := Read(path)
	if got.FiveHour.UsedPct != 11 || len(got.Earlier) != 1 || got.Earlier[0].FiveHour.UsedPct != 10 {
		t.Fatalf("got %+v, earlier %d", got.FiveHour, len(got.Earlier))
	}
}

package findings

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func report(fs ...Finding) Report { return Report{Findings: fs} }

func TestFilterHidesDismissedFindingsUntilTheyExpireOrRise(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	r := report(
		Finding{Key: "quota:openai:week", Kind: KindQuota, Level: LevelWarn, Title: "a"},
		Finding{Key: "waste:week", Kind: KindWaste, Level: LevelInfo, Title: "b"},
		Finding{Key: "boundary:headroom-pause", Kind: KindBoundary, Level: LevelNotice, Title: "c"},
	)
	ds := []Dismissal{
		{Key: "waste:week", Level: LevelInfo, Until: now.Add(time.Hour)},
		// Dismissed as a notice, it is a warn now: it shows again.
		{Key: "quota:openai:week", Level: LevelNotice, Until: now.Add(time.Hour)},
		// Dismissed while a warn, it is a notice now: it stays hidden.
		{Key: "boundary:headroom-pause", Level: LevelWarn, Until: now.Add(time.Hour)},
	}
	got := Filter(r, ds, now)
	if len(got.Findings) != 1 || got.Findings[0].Key != "quota:openai:week" || got.Hidden != 2 {
		t.Fatalf("filtered = %v hidden %d", keys(got), got.Hidden)
	}
	// Past its time, a dismissal hides nothing.
	if got := Filter(r, ds, now.Add(2*time.Hour)); len(got.Findings) != 3 || got.Hidden != 0 {
		t.Fatalf("expired: %v hidden %d", keys(got), got.Hidden)
	}
	// The report filtered is not changed.
	if len(r.Findings) != 3 {
		t.Fatal("Filter changed its input")
	}
}

func TestDismissalsPersistPrivatelyAndRestore(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	d := Dismissals{Path: filepath.Join(t.TempDir(), "state", "findings-dismissed.json")}
	if got, err := d.Load(now); err != nil || len(got) != 0 {
		t.Fatalf("empty store = %v, %v", got, err)
	}
	if err := d.Dismiss("waste:week", LevelInfo, now.Add(24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if err := d.Dismiss("coach:tips-waited", LevelInfo, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	// Dismissing again replaces the earlier one.
	if err := d.Dismiss("waste:week", LevelNotice, now.Add(7*24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(d.Path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", info, err)
	}
	got, err := d.Load(now)
	if err != nil || len(got) != 2 {
		t.Fatalf("loaded = %+v, %v", got, err)
	}
	for _, x := range got {
		if x.Key == "waste:week" && (x.Level != LevelNotice || !x.Until.Equal(now.Add(7*24*time.Hour))) {
			t.Errorf("replaced dismissal = %+v", x)
		}
	}
	// An expired one is dropped on read and on the next write.
	if got, _ := d.Load(now.Add(2 * time.Hour)); len(got) != 1 {
		t.Errorf("after expiry = %+v", got)
	}
	n, err := d.Restore("waste:week", now)
	if err != nil || n != 1 {
		t.Fatalf("restore one = %d, %v", n, err)
	}
	if got, _ := d.Load(now); len(got) != 1 || got[0].Key != "coach:tips-waited" {
		t.Fatalf("after restore = %+v", got)
	}
	if n, err := d.Restore("", now); err != nil || n != 1 {
		t.Fatalf("restore all = %d, %v", n, err)
	}
	if got, _ := d.Load(now); len(got) != 0 {
		t.Fatalf("after restore all = %+v", got)
	}
}

// A store whose file is damaged hides nothing rather than failing the
// findings.
func TestDamagedDismissalsHideNothing(t *testing.T) {
	d := Dismissals{Path: filepath.Join(t.TempDir(), "findings-dismissed.json")}
	if err := os.WriteFile(d.Path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Load(time.Now()); err == nil {
		t.Fatal("a damaged file read as empty without saying so")
	}
	// Dismissing over a damaged file starts it again.
	if err := d.Dismiss("waste:week", LevelInfo, time.Now().Add(time.Hour), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Load(time.Now()); err != nil || len(got) != 1 {
		t.Fatalf("after rewrite = %+v, %v", got, err)
	}
}

func TestDismissFor(t *testing.T) {
	for word, want := range map[string]time.Duration{"day": 24 * time.Hour, "week": 7 * 24 * time.Hour} {
		if got, ok := DismissFor(word); !ok || got != want {
			t.Errorf("%s = %v %v", word, got, ok)
		}
	}
	for _, word := range []string{"", "none", "month", "forever"} {
		if _, ok := DismissFor(word); ok {
			t.Errorf("%q accepted", word)
		}
	}
}

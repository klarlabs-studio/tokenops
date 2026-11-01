package boundaryledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An extra is active from its grant until it ends, on its own or early,
// and covers only the rules it names.
func TestExtraActiveAndCovers(t *testing.T) {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	ended := at.Add(10 * time.Minute)
	tests := []struct {
		name   string
		extra  Extra
		now    time.Time
		rule   string
		active bool
		covers bool
	}{
		{"inside", Extra{Rules: []string{"headroom-pause"}, At: at, Until: at.Add(time.Hour)}, at.Add(time.Minute), "headroom-pause", true, true},
		{"another rule", Extra{Rules: []string{"headroom-pause"}, At: at, Until: at.Add(time.Hour)}, at.Add(time.Minute), "session-spend", true, false},
		{"at its end", Extra{Rules: []string{"headroom-pause"}, At: at, Until: at.Add(time.Hour)}, at.Add(time.Hour), "headroom-pause", false, false},
		{"before its grant", Extra{Rules: []string{"headroom-pause"}, At: at, Until: at.Add(time.Hour)}, at.Add(-time.Minute), "headroom-pause", false, false},
		{"ended early", Extra{Rules: []string{"headroom-pause"}, At: at, Until: at.Add(time.Hour), EndedAt: &ended}, at.Add(20 * time.Minute), "headroom-pause", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.extra.Active(tc.now); got != tc.active {
				t.Errorf("active = %v, want %v", got, tc.active)
			}
			if got := tc.extra.Covers(tc.rule, tc.now); got != tc.covers {
				t.Errorf("covers = %v, want %v", got, tc.covers)
			}
		})
	}
}

// Grants are kept newest first, a week after they end; ending them stops
// every active one; an unreadable file lifts nothing.
func TestExtrasFile(t *testing.T) {
	d := Dir(t.TempDir())
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	old := Extra{ID: "bnd-000000000001", Rules: []string{"a"}, At: now.Add(-10 * 24 * time.Hour), Until: now.Add(-9 * 24 * time.Hour)}
	recent := Extra{ID: "bnd-000000000002", Rules: []string{"a"}, At: now.Add(-2 * time.Hour), Until: now.Add(-time.Hour)}
	live := Extra{ID: "bnd-000000000003", Rules: []string{"a"}, At: now.Add(-time.Minute), Until: now.Add(time.Hour)}
	for _, e := range []Extra{old, recent, live} {
		if err := d.PutExtra(e, now); err != nil {
			t.Fatal(err)
		}
	}
	all, err := d.Extras()
	if err != nil || len(all) != 2 || all[0].ID != live.ID || all[1].ID != recent.ID {
		t.Fatalf("extras = %+v, %v", all, err)
	}
	if active := d.ActiveExtras(now); len(active) != 1 || active[0].ID != live.ID {
		t.Fatalf("active = %+v", active)
	}
	ended, err := d.EndExtras(now)
	if err != nil || len(ended) != 1 || ended[0].ID != live.ID {
		t.Fatalf("ended = %+v, %v", ended, err)
	}
	if active := d.ActiveExtras(now); len(active) != 0 {
		t.Fatalf("still active after ending: %+v", active)
	}
	if err := os.WriteFile(filepath.Join(string(d), extrasFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if active := d.ActiveExtras(now); active != nil {
		t.Errorf("an unreadable file lifted %+v", active)
	}
}

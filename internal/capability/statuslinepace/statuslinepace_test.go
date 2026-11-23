package statuslinepace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

var at = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

// week is a weekly window reset in 3 days, used pct by now.
func week(used float64) plans.VendorWindow {
	return plans.VendorWindow{Name: "week", UsedPct: used, Duration: 7 * 24 * time.Hour, ResetsAt: at.Add(72 * time.Hour)}
}

func TestBuildKeepsTheGlanceForecast(t *testing.T) {
	recent := week(70)
	recent.Pace = &plans.WindowPace{Basis: plans.BasisRecent, RunsOutIn: 5 * time.Hour}
	cases := []struct {
		name    string
		win     plans.VendorWindow
		want    bool
		out     time.Time
		rate    string
		skipped bool
	}{
		// 4 of 7 days gone and 90% used: it runs out before its reset.
		{name: "runs out at this pace", win: week(90), want: true, rate: "at this pace"},
		{name: "the last hour's rate", win: recent, want: true, out: at.Add(5 * time.Hour), rate: "at the last hour's rate"},
		{name: "lasts to its reset", win: week(20)},
		{name: "stale", win: func() plans.VendorWindow { w := week(90); w.Stale = true; return w }(), skipped: true},
		{name: "lapsed", win: func() plans.VendorWindow { w := week(90); w.Lapsed = true; return w }(), skipped: true},
		{name: "reset passed", win: func() plans.VendorWindow { w := week(90); w.ResetsAt = at.Add(-time.Minute); return w }(), skipped: true},
		{name: "no reset", win: func() plans.VendorWindow { w := week(90); w.ResetsAt = time.Time{}; return w }(), skipped: true},
		{name: "another limit", win: func() plans.VendorWindow { w := week(90); w.VendorLabel = "codex_other"; return w }(), skipped: true},
		{name: "a model's week", win: func() plans.VendorWindow { w := week(90); w.Name = "week (Fable)"; return w }(), skipped: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := Build([]plans.HeadroomReport{{Provider: "anthropic", Windows: []plans.VendorWindow{c.win}}}, at, "1.2.3")
			if !f.ComputedAt.Equal(at) || f.Version != "1.2.3" {
				t.Fatalf("header %+v", f)
			}
			if c.skipped {
				if len(f.Windows) != 0 {
					t.Fatalf("kept %+v", f.Windows)
				}
				return
			}
			if len(f.Windows) != 1 {
				t.Fatalf("windows %+v", f.Windows)
			}
			w := f.Windows[0]
			if w.Provider != "anthropic" || w.Name != "week" || !w.ResetsAt.Equal(c.win.ResetsAt) {
				t.Fatalf("window %+v", w)
			}
			if got := !w.RunsOutAt.IsZero(); got != c.want {
				t.Fatalf("runs out %v, want %v", w.RunsOutAt, c.want)
			}
			if !c.want {
				return
			}
			if !w.RunsOutAt.Before(w.ResetsAt) || !w.RunsOutAt.After(at) || w.Rate != c.rate {
				t.Errorf("forecast %+v", w)
			}
			if c.win.Pace != nil && !w.RunsOutAt.Equal(c.out) {
				t.Errorf("runs out %v, want %v", w.RunsOutAt, c.out)
			}
		})
	}
}

func TestBuildIsBounded(t *testing.T) {
	reports := make([]plans.HeadroomReport, 0, 3*MaxWindows)
	for i := range 3 * MaxWindows {
		reports = append(reports, plans.HeadroomReport{Provider: "p" + strings.Repeat("x", i), Windows: []plans.VendorWindow{week(90), week(90)}})
	}
	f := Build(reports, at, "v")
	if len(f.Windows) != MaxWindows {
		t.Fatalf("%d windows", len(f.Windows))
	}
	// One provider's window once, however many plans name it.
	f = Build([]plans.HeadroomReport{{Provider: "anthropic", Windows: []plans.VendorWindow{week(90)}}, {Provider: "anthropic", Windows: []plans.VendorWindow{week(20)}}}, at, "v")
	if len(f.Windows) != 1 || f.Windows[0].RunsOutAt.IsZero() {
		t.Fatalf("%+v", f.Windows)
	}
}

func TestWriteIsAtomicPrivateAndBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", FileName)
	f := Build([]plans.HeadroomReport{{Provider: "anthropic", Windows: []plans.VendorWindow{week(90)}}}, at, "v")
	if err := Write(path, f); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("left behind %v", entries)
	}
	got, ok := Read(path)
	if !ok || len(got.Windows) != 1 || !got.Windows[0].RunsOutAt.Equal(f.Windows[0].RunsOutAt) {
		t.Fatalf("read back %+v %v", got, ok)
	}
	// A file too large to be one the daemon wrote is not written, nor read.
	huge := File{ComputedAt: at, Version: strings.Repeat("x", MaxSize)}
	if err := Write(path, huge); err == nil {
		t.Error("an oversized file was written")
	}
	if b, _ := json.Marshal(huge); os.WriteFile(path, b, 0o600) == nil {
		if _, ok := Read(path); ok {
			t.Error("an oversized file was read")
		}
	}
}

func TestRunsOut(t *testing.T) {
	reset := at.Add(72 * time.Hour)
	out := at.Add(30 * time.Hour)
	good := File{ComputedAt: at, Windows: []Window{
		{Provider: "anthropic", Name: "week", ResetsAt: reset, RunsOutAt: out, Rate: "at this pace"},
		{Provider: "anthropic", Name: "5h", ResetsAt: at.Add(time.Hour)},
		{Provider: "openai", Name: "5h", ResetsAt: at.Add(2 * time.Hour), RunsOutAt: at.Add(time.Hour)},
	}}
	cases := []struct {
		name     string
		f        File
		provider string
		window   string
		reset    time.Time
		now      time.Time
		want     bool
	}{
		{name: "fresh and the same window", f: good, provider: "anthropic", window: "week", reset: reset, now: at.Add(5 * time.Minute), want: true},
		{name: "the same reset to the second's rounding", f: good, provider: "anthropic", window: "week", reset: reset.Add(-40 * time.Second), now: at, want: true},
		{name: "at the freshness bound", f: good, provider: "anthropic", window: "week", reset: reset, now: at.Add(FreshFor), want: true},
		{name: "stale", f: good, provider: "anthropic", window: "week", reset: reset, now: at.Add(FreshFor + time.Second)},
		{name: "from the future", f: good, provider: "anthropic", window: "week", reset: reset, now: at.Add(-5 * time.Minute)},
		{name: "another window (a different reset)", f: good, provider: "anthropic", window: "week", reset: reset.Add(7 * 24 * time.Hour), now: at},
		{name: "lasts to its reset", f: good, provider: "anthropic", window: "5h", reset: at.Add(time.Hour), now: at},
		{name: "another provider's window", f: good, provider: "anthropic", window: "5h", reset: at.Add(2 * time.Hour), now: at},
		{name: "the run-out time has passed", f: good, provider: "anthropic", window: "week", reset: reset, now: at.Add(FreshFor), want: true},
		{name: "no reset on the line", f: good, provider: "anthropic", window: "week", now: at},
		{name: "empty file", f: File{}, provider: "anthropic", window: "week", reset: reset, now: at},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rate, ok := c.f.RunsOut(c.provider, c.window, c.reset, c.now)
			if ok != c.want {
				t.Fatalf("ok %v, want %v", ok, c.want)
			}
			if ok && (!got.Equal(out) || rate != "at this pace") {
				t.Errorf("got %v %q", got, rate)
			}
		})
	}
	passed := File{ComputedAt: at, Windows: []Window{{Provider: "anthropic", Name: "week", ResetsAt: reset, RunsOutAt: at.Add(time.Minute)}}}
	if _, _, ok := passed.RunsOut("anthropic", "week", reset, at.Add(2*time.Minute)); ok {
		t.Error("a run-out time already past is shown")
	}
}

func TestReadFailsClosed(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"missing":   "",
		"malformed": "{not json",
		"no time":   `{"windows":[{"provider":"anthropic","name":"week"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
			if body != "" {
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if f, ok := Read(path); ok {
				t.Errorf("read %+v", f)
			}
		})
	}
}

// clock is a test clock safe to move while a keeper runs.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// A keeper writes the forecast at start and at each tick, with no request
// behind it, so the status line finds it fresh while nothing asks the
// daemon for the glance.
func TestKeeperWritesWithoutRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	now := &clock{t: at}
	var calls int
	k := Keeper{
		Path: path, Version: "v",
		Now: now.Now,
		Source: func(context.Context, time.Time) ([]plans.HeadroomReport, error) {
			calls++
			return []plans.HeadroomReport{{Provider: "anthropic", Windows: []plans.VendorWindow{week(90)}}}, nil
		},
		Warn: func(err error) { t.Errorf("warned %v", err) },
	}
	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { k.Run(ctx, tick); close(done) }()
	last := at
	for range 3 {
		last = last.Add(Every)
		now.Set(last)
		tick <- last
	}
	cancel()
	<-done
	if calls != 4 {
		t.Fatalf("computed %d times, want the start and each of 3 ticks", calls)
	}
	got, ok := Read(path)
	if !ok || !got.ComputedAt.Equal(last) || len(got.Windows) != 1 {
		t.Fatalf("file %+v ok=%v, want computed at %v", got, ok, last)
	}
}

// A failing write is reported once per failure, not once per tick, and
// a cancelled run is not a failure.
func TestKeeperReportsAFailureOnce(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var warnings []error
	k := Keeper{
		Path: filepath.Join(t.TempDir(), FileName), Version: "v",
		Now: func() time.Time { return at },
		Source: func(context.Context, time.Time) ([]plans.HeadroomReport, error) {
			if fail.Load() {
				return nil, errors.New("events unreadable")
			}
			return nil, nil
		},
		Warn: func(err error) { warnings = append(warnings, err) },
	}
	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { k.Run(ctx, tick); close(done) }()
	tick <- at // failing: start and this tick
	tick <- at
	fail.Store(false)
	tick <- at // recovers
	fail.Store(true)
	tick <- at // fails again: reported again
	cancel()
	<-done
	if len(warnings) != 2 {
		t.Fatalf("warned %d times, want once per failure episode: %v", len(warnings), warnings)
	}
}

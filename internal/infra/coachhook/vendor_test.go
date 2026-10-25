package coachhook

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

func usedUp(provider string, resetsIn time.Duration, at time.Time) *Quota {
	w := plans.QuotaWindow{Label: "5-hour", UsedPct: 100, ResetsAt: at.Add(resetsIn), Duration: 5 * time.Hour}
	return &Quota{Provider: provider, Window: w, All: []plans.QuotaWindow{w}}
}

func otherPlan(usedPct float64) func() []Alternative {
	return func() []Alternative {
		return []Alternative{{Provider: "openai", Window: plans.QuotaWindow{Label: "5-hour", UsedPct: usedPct, ResetsAt: fixedNow.Add(time.Hour)}}}
	}
}

func standDownFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	return dir, writeTranscript(t, dir, turnLine(ts(1), 1_000, opus))
}

// Claude Code answers a used-up window with its own limit message, so a
// tip that only says the same stands down, and says why in the ledger.
func TestUsedUpStandsDownWhereClaudeCodeSpeaks(t *testing.T) {
	dir, tp := standDownFixture(t)
	cfg := tipConfig()
	cfg.Quota = usedUp("anthropic", 3*time.Hour, fixedNow)
	d := Evaluate(dir, "s1", tp, cfg, fixedNow)
	if d.Nudge || d.Message != "" || d.TipKind != "" {
		t.Fatalf("restated the vendor's limit message: %+v", d)
	}
	if d.Suppressed != SuppressedVendorSpeaks || d.StoodDown != "quota_100" || d.VendorSurface == "" {
		t.Fatalf("stand-down not recorded with its reason: %+v", d)
	}
	// Once per window, like the tip it replaces.
	Evaluate(dir, "s2", tp, cfg, fixedNow.Add(time.Minute))
	s, err := ReadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.StoodDown["quota_100"] != 1 || s.Suppressed[SuppressedVendorSpeaks] != 1 {
		t.Fatalf("stats = %+v / %+v", s.StoodDown, s.Suppressed)
	}
}

// What the vendor does not say is kept: another plan with room.
func TestUsedUpKeptWhenAnotherPlanHasRoom(t *testing.T) {
	dir, tp := standDownFixture(t)
	cfg := tipConfig()
	cfg.Quota = usedUp("anthropic", 3*time.Hour, fixedNow)
	cfg.Alternatives = otherPlan(40)
	d := Evaluate(dir, "s1", tp, cfg, fixedNow)
	if !d.Nudge || d.TipKind != "quota_100" {
		t.Fatalf("cross-plan finding dropped: %+v", d)
	}
	for _, want := range []string{"is used up", "Another plan has room", "5-hour OpenAI limit", "60% left"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q missing %q", d.Message, want)
		}
	}
}

// A plan that is nearly spent is no place to move work to.
func TestUsedUpStandsDownWhenOtherPlanIsAlsoTight(t *testing.T) {
	dir, tp := standDownFixture(t)
	cfg := tipConfig()
	cfg.Quota = usedUp("anthropic", 3*time.Hour, fixedNow)
	cfg.Alternatives = otherPlan(90)
	if d := Evaluate(dir, "s1", tp, cfg, fixedNow); d.Nudge || d.Suppressed != SuppressedVendorSpeaks {
		t.Fatalf("%+v", d)
	}
}

// A harness without the vendor surface is unaffected: Codex has no
// Claude Code limit message, and verbose asks to hear everything.
func TestUsedUpSpokenWithoutTheVendorSurface(t *testing.T) {
	t.Run("codex", func(t *testing.T) {
		dir, tp := standDownFixture(t)
		cfg := tipConfig()
		cfg.Quota = usedUp("openai", 3*time.Hour, fixedNow)
		if d := Evaluate(dir, "s1", tp, cfg, fixedNow); !d.Nudge || d.StoodDown != "" {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("verbose", func(t *testing.T) {
		dir, tp := standDownFixture(t)
		cfg := tipConfig()
		cfg.Verbosity = verbosityVerbose
		cfg.Quota = usedUp("anthropic", 3*time.Hour, fixedNow)
		if d := Evaluate(dir, "s1", tp, cfg, fixedNow); !d.Nudge || d.StoodDown != "" {
			t.Fatalf("%+v", d)
		}
	})
}

// The weekly pace banner is seen in the desktop app and not verified in
// Claude Code, so it stands nothing down: the run-out warning is only
// rate-limited, and only where the ledger shows it mostly goes unseen.
func TestUnverifiedSurfaceNeverStandsDown(t *testing.T) {
	for _, s := range vendorSurfaces {
		if s.Verified {
			continue
		}
		dir, tp := standDownFixture(t)
		cfg := tipConfig()
		cfg.Quota = burningQuota(60, 2*time.Hour, 80*time.Minute)
		cfg.MostlyUnseen = func(string) bool { return true }
		d := Evaluate(dir, "s1", tp, cfg, fixedNow)
		if !d.Nudge || !d.QuotaBurn || d.StoodDown != "" {
			t.Fatalf("%s: unverified surface stood the tip down: %+v", s.Surface, d)
		}
	}
}

func burnWindow(at time.Time) *Quota {
	w := plans.QuotaWindow{Label: "5-hour", UsedPct: 60, ResetsAt: at.Add(2 * time.Hour), Duration: 5 * time.Hour}
	return &Quota{Provider: "anthropic", Window: w, All: []plans.QuotaWindow{w}, Burn: plans.BurnVerdict{
		Warnings: []plans.BurnWarning{{Window: w, RunsOutAt: at.Add(80 * time.Minute), LongBurn: 1.5, ShortBurn: 1.6}},
	}}
}

func TestMostlyUnseenBurnInterruptsOncePerDay(t *testing.T) {
	cases := []struct {
		name   string
		unseen bool
		want   []string // per window: "nudge" or "ambient"
	}{
		{"mostly unseen", true, []string{"nudge", "ambient", "ambient", "nudge"}},
		{"seen", false, []string{"nudge", "nudge", "nudge", "nudge"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, tp := standDownFixture(t)
			cfg := tipConfig()
			cfg.MostlyUnseen = func(kind string) bool { return c.unseen && kind == burnKind }
			// Three windows on one day, one on the next.
			// Local hours: the day is the operator's.
			day := time.Date(2026, 7, 7, 6, 0, 0, 0, time.Local)
			starts := []time.Time{day, day.Add(5 * time.Hour), day.Add(10 * time.Hour), day.Add(24 * time.Hour)}
			for i, at := range starts {
				cfg.Quota = burnWindow(at)
				d := Evaluate(dir, fmt.Sprint("s", i), tp, cfg, at)
				got := "nothing"
				switch {
				case d.Nudge:
					got = "nudge"
				case d.Ambient && d.Suppressed == SuppressedMostlyUnseen:
					got = "ambient"
				}
				if got != c.want[i] {
					t.Errorf("window %d: %s, want %s (%+v)", i, got, c.want[i], d)
				}
			}
		})
	}
}

// tally counts what one Stop did.
type tally struct{ interrupts, ambient, standDown int }

func (t *tally) add(d Decision) {
	switch {
	case d.Nudge:
		t.interrupts++
	case d.Ambient:
		t.ambient++
	case d.StoodDown != "":
		t.standDown++
	}
}

// replayWeek replays a week of a Claude Code user who runs a 5-hour window
// out twice a day: a run-out warning, then the window used up.
func replayWeek(t *testing.T, surfaces []vendorSurface, unseen func(string) bool) (burn, used tally) {
	t.Helper()
	saved := vendorSurfaces
	vendorSurfaces = surfaces
	defer func() { vendorSurfaces = saved }()
	dir, tp := standDownFixture(t)
	cfg := tipConfig()
	cfg.MostlyUnseen = unseen
	base := time.Date(2026, 10, 12, 6, 0, 0, 0, time.Local)
	for day := range 7 {
		for win := range 2 {
			at := base.Add(time.Duration(day)*24*time.Hour + time.Duration(win)*7*time.Hour)
			cfg.Quota = burnWindow(at)
			burn.add(Evaluate(dir, fmt.Sprint("b", day, win), tp, cfg, at))
			cfg.Quota = burnWindow(at)
			cfg.Quota.Window.UsedPct, cfg.Quota.Burn = 100, plans.BurnVerdict{}
			used.add(Evaluate(dir, fmt.Sprint("u", day, win), tp, cfg, at.Add(time.Hour)))
		}
	}
	return burn, used
}

// The measure: the same week with and without the rules. Printed so the
// pull request can quote it; asserted so it cannot drift.
func TestStandDownCutsTheWeek(t *testing.T) {
	beforeBurn, beforeUsed := replayWeek(t, nil, nil)
	afterBurn, afterUsed := replayWeek(t, vendorSurfaces, func(k string) bool { return k == burnKind })
	t.Logf("run-out warnings   before: %+v  after: %+v", beforeBurn, afterBurn)
	t.Logf("used-up tips       before: %+v  after: %+v", beforeUsed, afterUsed)
	before := beforeBurn.interrupts + beforeUsed.interrupts
	after := afterBurn.interrupts + afterUsed.interrupts
	t.Logf("interruptions in the week: %d -> %d", before, after)
	if beforeBurn.interrupts != 14 || beforeUsed.interrupts != 14 {
		t.Fatalf("baseline week = %+v / %+v, want 14 and 14", beforeBurn, beforeUsed)
	}
	if afterBurn.interrupts != 7 || afterBurn.ambient != 7 {
		t.Errorf("run-out warnings after = %+v, want one interruption a day and the rest on the status line", afterBurn)
	}
	if afterUsed.interrupts != 0 || afterUsed.standDown != 14 {
		t.Errorf("used-up tips after = %+v, want all 14 stood down", afterUsed)
	}
}

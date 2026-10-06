package findings

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

func glance() *headroom.Glance {
	var g headroom.Glance
	g.Headroom.Reports = []plans.HeadroomReport{
		{Provider: "openai", Windows: []plans.VendorWindow{{Name: "week", UsedPct: 55, ResetsIn: "138h0m0s",
			Pace: &plans.WindowPace{Status: plans.PaceAhead, DeltaPct: 37, RunsOutIn: 24 * time.Hour}}}},
		{Provider: "anthropic", Windows: []plans.VendorWindow{{Name: "5h", UsedPct: 33,
			Pace: &plans.WindowPace{Status: plans.PaceBehind, DeltaPct: -20, LastsToReset: true}}}},
	}
	return &g
}

func offCoach() *coach.Report {
	return &coach.Report{
		Powers:        []coach.Power{{Name: config.PowerWaste, Effective: config.AutonomyOff}},
		FollowThrough: []ft.Summary{{Power: "inform", Kind: "compact_now", Followed: 1, Ignored: 4, Quiet: true}},
	}
}

func TestComputeRanksEveryFinding(t *testing.T) {
	at := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	r := Compute(Inputs{
		Glance:    glance(),
		Coach:     offCoach(),
		CoachHook: &coachhook.Stats{DistinctSessions: 97, AlertsByTier: map[string]int{"100%": 44}, MaxCumulativeUSD: 1900.45, Alerts: 556, CompactTips: 3, QuotaNudges: map[string]int{"50%": 1}},
		ReadGuard: &readguard.Stats{WouldBlock: 14, ReclaimableTok: 225_000, DistinctSessions: 63, Blocked: 255, ReclaimedTok: 1_227_548},
		Sessions: &SessionsSnapshot{ComputedAt: at, DX: sessions.DX{
			Warnings:       []string{"agentdx: unrecognised opencode database schema: no such table: session_v2"},
			Recommendation: &sessions.Recommendation{Title: "Instructions take a lot of turns", Evidence: "p90 46 turns, median 4", Action: "split the long ones"},
		}},
	})
	titles := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		titles = append(titles, f.Level+" "+f.Title)
	}
	want := []string{
		"warn Codex's weekly window runs out in 1d 0h at this pace",
		"warn opencode sessions cannot be read",
		"notice Instructions take a lot of turns",
		"notice Agents re-read 14 unchanged files in full",
		"notice 44 of 97 sessions ran past their budget",
		"info The read guard refused 255 re-reads",
		"info The coach is observing and says nothing",
		"info The coach went quiet on compact tips",
	}
	if strings.Join(titles, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(titles, "\n"), strings.Join(want, "\n"))
	}
	f := r.Findings[0]
	if !strings.Contains(f.Action, "Claude (67% left)") || !strings.Contains(f.Evidence, "resets in 5d 18h") {
		t.Errorf("quota finding: %+v", f)
	}
	if !strings.Contains(r.Findings[1].Evidence, "newer format") {
		t.Errorf("opencode finding: %+v", r.Findings[1])
	}
	if r.Findings[2].Action != "Split the long ones." {
		t.Errorf("recommendation action: %q", r.Findings[2].Action)
	}
	if !strings.Contains(r.Findings[6].Evidence, "560 tips") {
		t.Errorf("observing coach: %+v", r.Findings[6])
	}
	if r.SessionsReadAt == nil || !r.SessionsReadAt.Equal(at) {
		t.Errorf("sessions read at %v", r.SessionsReadAt)
	}
}

// A window already used up is said to be, with when it comes back; it
// does not "run out in 0m".
func TestUsedUpWindow(t *testing.T) {
	g := glance()
	g.Headroom.Reports[0].Windows[0] = plans.VendorWindow{Name: "week", UsedPct: 100, ResetsIn: "85h46m0s",
		Pace: &plans.WindowPace{Status: plans.PaceUsedUp, DeltaPct: 51}}
	r := Compute(Inputs{Glance: g})
	if len(r.Findings) != 1 {
		t.Fatalf("findings %+v", r.Findings)
	}
	f := r.Findings[0]
	if f.Kind != KindQuota || f.Level != LevelWarn || f.Title != "Codex's weekly window is used up until it resets in 3d 13h" ||
		!strings.Contains(f.Action, "Claude (67% left)") {
		t.Errorf("used-up finding %+v", f)
	}
}

// A plan whose every window is stale says so, once, with how to get the
// reading back, and its stale windows warn about nothing else (ADR 0011).
func TestStalePlanReading(t *testing.T) {
	g := glance()
	at := time.Now().Add(-20 * time.Hour)
	g.Headroom.Reports[1].Windows = []plans.VendorWindow{
		{Name: "5h", UsedPct: 99, Source: "claude-usage-meter", ObservedAt: at, Stale: true,
			Pace: &plans.WindowPace{Status: plans.PaceAhead, RunsOutIn: time.Hour}},
		{Name: "week", UsedPct: 7, Source: "claude-usage-meter", ObservedAt: at, Stale: true},
	}
	r := Compute(Inputs{Glance: g})
	titles := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		titles = append(titles, f.Title)
		if f.Kind == KindQuota && strings.HasPrefix(f.Title, "Claude's session") {
			t.Errorf("a stale window warned: %+v", f)
		}
	}
	want := "Claude's plan reading is 20h old"
	found := false
	for _, f := range r.Findings {
		if f.Title == want {
			found = true
			if f.Level != LevelWarn || !strings.Contains(f.Action, "claude.ai") {
				t.Errorf("stale finding %+v", f)
			}
		}
	}
	if !found {
		t.Errorf("no %q in %v", want, titles)
	}
}

// A coach that already refuses re-reads is not told to; a plan with no
// room is not offered as the place to move work; nothing is an empty
// list, never null.
func TestComputeStaysQuietWhereNothingApplies(t *testing.T) {
	g := glance()
	g.Headroom.Reports[1].Windows[0].UsedPct = 90
	autonomous := &coach.Report{Powers: []coach.Power{{Name: config.PowerWaste, Effective: config.AutonomyAutonomous}}}
	r := Compute(Inputs{Glance: g, Coach: autonomous, ReadGuard: &readguard.Stats{WouldBlock: 3}})
	for _, f := range r.Findings {
		if f.Kind == KindWaste {
			t.Errorf("waste finding under an autonomous guard: %+v", f)
		}
		if f.Kind == KindQuota && strings.Contains(f.Action, "Claude") {
			t.Errorf("work sent to a plan with no room: %+v", f)
		}
	}
	if empty := Compute(Inputs{}); empty.Findings == nil || len(empty.Findings) != 0 {
		t.Errorf("empty = %+v", empty)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if s, err := ReadSnapshot(dir); s != nil || err != nil {
		t.Fatalf("no snapshot yet: %v %v", s, err)
	}
	want := SessionsSnapshot{ComputedAt: time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC), DX: sessions.DX{Window: "last 7d"}}
	if err := WriteSnapshot(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSnapshot(dir)
	if err != nil || got == nil || !got.ComputedAt.Equal(want.ComputedAt) || got.DX.Window != "last 7d" {
		t.Fatalf("read back %+v %v", got, err)
	}
}

// The API and MCP answer the default week from the daemon's analysis when
// it is recent, and read the transcripts otherwise.
func TestDXUsesARecentAnalysis(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	snap := SessionsSnapshot{ComputedAt: now.Add(-time.Hour)}
	snap.DX.Window = "cached"
	snap.DX.Metrics.Prompts = 9
	if err := WriteSnapshot(DefaultDir(), snap); err != nil {
		t.Fatal(err)
	}
	if got := DX(sessions.Window{}, now); got.Window != "cached" {
		t.Errorf("default week = %q, want the analysis", got.Window)
	}
	if got := DX(sessions.Window{Days: 30}, now); got.Window == "cached" {
		t.Error("a 30-day window used the week's analysis")
	}
	if got := DX(sessions.Window{}, now.Add(MaxSnapshotAge+time.Hour)); got.Window == "cached" {
		t.Error("a stale analysis was used")
	}
}

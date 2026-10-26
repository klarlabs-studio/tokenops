package findings

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/capability/anomalies"
	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/spend/anomaly"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// memLedger is a follow-through ledger in memory.
type memLedger struct{ entries []ft.Entry }

func (m *memLedger) Append(e ...ft.Entry) error { m.entries = append(m.entries, e...); return nil }
func (m *memLedger) Load() ([]ft.Entry, error)  { return m.entries, nil }

func shadowPause() *boundaries.PauseSummary {
	return &boundaries.PauseSummary{Configured: true, Rule: "headroom-pause", Stage: "shadow", Sessions: 3, PausePoint: true,
		When: "the weekly window has 20% or less left", Require: "ask"}
}

func openAnomaly() anomalies.Anomaly {
	return anomalies.Anomaly{Anomaly: anomaly.Anomaly{ID: "anomaly-spend-2026-10-14", Metric: anomaly.MetricSpend,
		Day: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), ActualUSD: 24, ExcessUSD: 24}}
}

// everyKind is inputs that raise a finding of nearly every kind.
func everyKind() Inputs {
	return Inputs{
		Glance:    glance(),
		Coach:     offCoach(),
		CoachHook: &coachhook.Stats{DistinctSessions: 9, AlertsByTier: map[string]int{"100%": 2}, Suppressed: map[string]int{coachhook.SuppressedDailyBudget: 3}},
		ReadGuard: &readguard.Stats{WouldBlock: 4, ReclaimableTok: 1000, DistinctSessions: 2, Blocked: 5, ReclaimedTok: 900},
		Sessions: &SessionsSnapshot{DX: sessions.DX{
			Warnings:       []string{"agentdx: unrecognised opencode database schema"},
			Recommendation: &sessions.Recommendation{Title: "Instructions take 46 turns", Action: "split them"},
		}},
		Anomalies: []anomalies.Anomaly{openAnomaly()},
		Pause:     shadowPause(),
	}
}

var keyShape = regexp.MustCompile(`^[a-z0-9][a-z0-9:._-]{0,127}$`)

// Every finding carries a key of a closed charset, unique in the report,
// and naming its kind first; no figure that changes between readings is
// in it.
func TestEveryFindingHasAStableKey(t *testing.T) {
	r := Compute(everyKind())
	seen := map[string]bool{}
	for _, f := range r.Findings {
		if !keyShape.MatchString(f.Key) {
			t.Errorf("%q: key %q is not of the closed charset", f.Title, f.Key)
		}
		if !strings.HasPrefix(f.Key, f.Kind+":") {
			t.Errorf("%q: key %q does not start with its kind %q", f.Title, f.Key, f.Kind)
		}
		if seen[f.Key] {
			t.Errorf("key %q is used twice", f.Key)
		}
		seen[f.Key] = true
	}
	for _, want := range []string{"quota:openai:week", "boundary:headroom-pause", "spend:anomaly-spend-2026-10-14",
		"coach:tips-waited", "waste:rereads", "budget:over", "data:opencode"} {
		if !seen[want] {
			t.Errorf("no finding keyed %q in %v", want, keys(r))
		}
	}

	// The same finding with other figures keeps its key.
	in := everyKind()
	in.Glance.Headroom.Reports[0].Windows[0].UsedPct = 80
	in.Glance.Headroom.Reports[0].Windows[0].Pace.RunsOutIn = time.Hour
	in.Pause.Sessions = 7
	in.ReadGuard.WouldBlock = 40
	in.Sessions.DX.Recommendation.Title = "Instructions take 52 turns"
	again := map[string]bool{}
	for _, k := range keys(Compute(in)) {
		again[k] = true
	}
	for k := range seen {
		if !again[k] {
			t.Errorf("key %q changed with the figures; now %v", k, keys(Compute(in)))
		}
	}
}

func keys(r Report) []string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, f.Key)
	}
	return out
}

func byKey(t *testing.T, r Report, key string) Finding {
	t.Helper()
	f, ok := r.Find(key)
	if !ok {
		t.Fatalf("no finding %q in %v", key, keys(r))
	}
	return f
}

func actionIDs(f Finding) []string {
	out := make([]string, 0, len(f.Actions))
	for _, a := range f.Actions {
		out = append(out, a.ID)
	}
	return out
}

// Only findings with an operation behind them offer actions, and each
// offers exactly the operations the CLI has for it. Promoting a boundary
// binds, so it asks first, in the rule's own words and with the undo.
func TestFindingsOfferTheActionsTheirCLIHas(t *testing.T) {
	r := Compute(everyKind())

	pause := byKey(t, r, "boundary:headroom-pause")
	if got := strings.Join(actionIDs(pause), ","); got != ActBoundaryPromote {
		t.Fatalf("pause actions = %s", got)
	}
	c := pause.Actions[0].Confirm
	for _, want := range []string{"headroom-pause", "the weekly window has 20% or less left", "tokenops boundaries demote headroom-pause"} {
		if !strings.Contains(c, want) {
			t.Errorf("promote confirm %q lacks %q", c, want)
		}
	}
	if strings.Contains(c, "10%") {
		t.Errorf("promote confirm %q assumes the default 10%%", c)
	}

	tips := []string{ActTipAck, ActTipExpected, ActTipSnooze}
	for _, key := range []string{"spend:anomaly-spend-2026-10-14", "coach:tips-waited"} {
		f := byKey(t, r, key)
		if got := strings.Join(actionIDs(f), ","); got != strings.Join(tips, ",") {
			t.Errorf("%s actions = %s", key, got)
		}
		for _, a := range f.Actions {
			if a.Label == "" {
				t.Errorf("%s %s has no label", key, a.ID)
			}
			// Expected changes what is flagged or how the budget is set
			// from here on, so it asks; ack and snooze do not.
			if (a.ID == ActTipExpected) != (a.Confirm != "") {
				t.Errorf("%s %s confirm = %q", key, a.ID, a.Confirm)
			}
		}
	}

	for _, f := range r.Findings {
		switch f.Key {
		case "boundary:headroom-pause", "spend:anomaly-spend-2026-10-14", "coach:tips-waited":
			continue
		}
		if len(f.Actions) != 0 {
			t.Errorf("%s offers %v with no operation behind it", f.Key, actionIDs(f))
		}
	}

	// Enforced, the pause offers nothing more to promote.
	in := everyKind()
	in.Pause.Stage = "enforce"
	for _, f := range Compute(in).Findings {
		if f.Kind == KindBoundary && len(f.Actions) != 0 {
			t.Errorf("an enforced pause offers %v", actionIDs(f))
		}
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const pauseConfig = `boundaries:
  - name: headroom-pause
    action: continue
    when:
      plan_left_pct: "<= 10"
    require: ask
    stage: shadow
`

func TestActPromotesTheBoundaryThroughTheSameCodeAsTheCLI(t *testing.T) {
	path := writeConfig(t, pauseConfig)
	r := Compute(Inputs{Glance: glance(), Pause: shadowPause()})
	got, err := Act(r, "boundary:headroom-pause", ActBoundaryPromote, ActEnv{ConfigPath: path, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rule != "headroom-pause" || !got.ConfigChanged || !strings.Contains(got.Note, "demote") {
		t.Errorf("acted = %+v", got)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil || cfg.Boundaries[0].Stage != "enforce" {
		t.Fatalf("stage = %+v, %v", cfg.Boundaries, err)
	}
}

func TestActAnswersTipsThroughTheLedger(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	l := &memLedger{}
	coach.RecordTip(l, now.Add(-time.Hour), "tip-1", "s1", "budget_75", coach.ViaStopHook)
	coach.RecordTip(l, now.Add(-2*time.Hour), "anomaly-spend-2026-10-14", "", anomalies.KindPrefix+"spike", coach.ViaStopHook)
	r := Compute(everyKind())

	// The waiting-tips finding answers the newest unanswered tip.
	got, err := Act(r, "coach:tips-waited", ActTipSnooze, ActEnv{Ledger: l, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tip != "tip-1" || got.SnoozedUntil == nil || !got.SnoozedUntil.Equal(now.Add(coach.DefaultSnooze)) || got.ConfigChanged {
		t.Errorf("snooze = %+v", got)
	}
	// A finding with an id answers that tip.
	got, err = Act(r, "spend:anomaly-spend-2026-10-14", ActTipExpected, ActEnv{Ledger: l, Now: now})
	if err != nil || got.Tip != "anomaly-spend-2026-10-14" {
		t.Fatalf("expected = %+v, %v", got, err)
	}
	recs := ft.Fold(l.entries, now)
	answers := map[string]ft.Outcome{}
	for _, rec := range recs {
		answers[rec.Offer.ID] = rec.Answer
	}
	if answers["tip-1"] != ft.OutcomeSnoozed || answers["anomaly-spend-2026-10-14"] != ft.OutcomeExpected {
		t.Errorf("ledger answers = %v", answers)
	}
	// Nothing left to answer is said plainly, as a stale finding.
	if _, err := Act(r, "coach:tips-waited", ActTipAck, ActEnv{Ledger: l, Now: now}); !errors.Is(err, ErrStale) {
		t.Errorf("answering with no tip left = %v, want ErrStale", err)
	}
}

// The daemon acts only on a finding it shows now, and only with an
// action that finding offers: a stale key, an action it does not offer
// and an action outside the allowlist are each refused, and nothing is
// written.
func TestActRefusesWhatTheFindingDoesNotOffer(t *testing.T) {
	path := writeConfig(t, pauseConfig)
	l := &memLedger{}
	r := Compute(everyKind())
	env := ActEnv{ConfigPath: path, Ledger: l, Now: time.Now()}
	for _, tc := range []struct {
		key, action string
		stale       bool
	}{
		{key: "boundary:nope", action: ActBoundaryPromote, stale: true},
		{key: "spend:anomaly-spend-2026-10-14", action: ActBoundaryPromote, stale: true},
		{key: "waste:rereads", action: ActTipAck, stale: true},
		{key: "boundary:headroom-pause", action: "boundary.delete"},
		{key: "boundary:headroom-pause", action: ""},
	} {
		_, err := Act(r, tc.key, tc.action, env)
		switch {
		case err == nil:
			t.Errorf("%s %s was run", tc.key, tc.action)
		case tc.stale && !errors.Is(err, ErrStale):
			t.Errorf("%s %s = %v, want ErrStale", tc.key, tc.action, err)
		case !tc.stale && !actions.IsInput(err):
			t.Errorf("%s %s = %v, want an input error", tc.key, tc.action, err)
		}
	}
	if cfg, _ := config.ReadMutable(path); cfg.Boundaries[0].Stage != "shadow" {
		t.Error("a refused action promoted the rule")
	}
	if len(l.entries) != 0 {
		t.Errorf("a refused action wrote the ledger: %v", l.entries)
	}
}

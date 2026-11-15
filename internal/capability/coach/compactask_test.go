package coach

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

// askLevers previews line until it is applied, as the real levers do.
type askLevers struct {
	line    int64
	written int64
	applied int
}

func (a *askLevers) Apply() ([]LeverResult, error) {
	a.applied++
	a.written = a.line
	return []LeverResult{{Client: "claude-code", Key: "env.CLAUDE_CODE_AUTO_COMPACT_WINDOW", Value: a.line, Status: "applied"}}, nil
}
func (a *askLevers) Revert() ([]LeverResult, error) { a.written = 0; return nil, nil }
func (a *askLevers) Check() []LeverResult           { return nil }
func (a *askLevers) Preview() ([]LeverResult, error) {
	status := LeverShadow
	if a.written == a.line {
		status = "active"
	}
	return []LeverResult{{Client: "claude-code", Key: "env.CLAUDE_CODE_AUTO_COMPACT_WINDOW", Value: a.line, Status: status}}, nil
}

type memSavings struct{ entries []learning.SavingsEntry }

func (m *memSavings) Append(es ...learning.SavingsEntry) error {
	m.entries = append(m.entries, es...)
	return nil
}
func (m *memSavings) Load() ([]learning.SavingsEntry, error) { return m.entries, nil }

func (m *memSavings) shadows() int {
	n := 0
	for _, e := range m.entries {
		if e.Kind == KindCompactLine && e.Cohort == learning.CohortShadow {
			n++
		}
	}
	return n
}

var askCfg = config.Config{Coach: config.CoachConfig{Autonomy: config.AutonomyAdvise,
	Powers: map[string]string{config.PowerContext: config.AutonomyAsk}}}

func proposals(l *memLedger) int {
	n := 0
	for _, e := range l.entries {
		if e.Type == ft.EntryOffer && isProposal(e) {
			n++
		}
	}
	return n
}

// At ask the line is proposed once and shadowed on every step; nothing is
// written before the developer accepts.
func TestAskProposesAndShadowsWithoutWriting(t *testing.T) {
	l, s, lv := &memLedger{}, &memSavings{}, &askLevers{line: 633_000}
	env := AskEnv{Ledger: l, Shadow: s, Levers: lv, Graduated: func() bool { return true }}
	StepCompaction(askCfg, env, now)
	StepCompaction(askCfg, env, now.Add(24*time.Hour))
	if lv.applied != 0 {
		t.Fatalf("wrote the line before it was accepted")
	}
	if got := proposals(l); got != 1 {
		t.Errorf("proposals = %d; want the same line put once", got)
	}
	if s.shadows() != 2 {
		t.Errorf("shadow records = %d; want one per step", s.shadows())
	}
	p, ok := PendingCompaction(l, now.Add(48*time.Hour))
	if !ok || !strings.Contains(p.Settings, "633000") || p.Command != AcceptCommand {
		t.Errorf("pending = %+v, %v; want the line, still open past a day, with the command", p, ok)
	}
}

// Accepting writes the line when the guardrail has graduated, and keeps it
// in shadow until then; the step after graduation writes it.
func TestAcceptWritesOnlyOnceGraduated(t *testing.T) {
	tests := []struct {
		name      string
		graduated bool
		written   bool
	}{
		{"graduated: written on the yes", true, true},
		{"not graduated: kept in shadow", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, s, lv := &memLedger{}, &memSavings{}, &askLevers{line: 633_000}
			grad := tt.graduated
			env := AskEnv{Ledger: l, Shadow: s, Levers: lv, Graduated: func() bool { return grad }}
			StepCompaction(askCfg, env, now)
			a, err := AcceptCompaction(env, "", now.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if a.Written != tt.written || (lv.applied == 1) != tt.written {
				t.Fatalf("written = %v, applied %d; want %v", a.Written, lv.applied, tt.written)
			}
			if !tt.written && a.Note == "" {
				t.Error("a yes held for graduation must say so")
			}
			if _, ok := PendingCompaction(l, now.Add(2*time.Hour)); ok {
				t.Error("an accepted proposal is still pending")
			}
			grad = true
			StepCompaction(askCfg, env, now.Add(24*time.Hour))
			if lv.applied != 1 {
				t.Errorf("applied %d times after graduation; want once", lv.applied)
			}
		})
	}
}

// A no is not asked again for the same line; a new line is proposed.
func TestDeclinedLineIsNotProposedAgain(t *testing.T) {
	l, s, lv := &memLedger{}, &memSavings{}, &askLevers{line: 633_000}
	env := AskEnv{Ledger: l, Shadow: s, Levers: lv}
	StepCompaction(askCfg, env, now)
	if _, err := DeclineCompaction(l, "", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	StepCompaction(askCfg, env, now.Add(24*time.Hour))
	if proposals(l) != 1 {
		t.Fatalf("a declined line was proposed again")
	}
	lv.line = 433_000
	StepCompaction(askCfg, env, now.Add(48*time.Hour))
	if proposals(l) != 2 || lv.applied != 0 {
		t.Errorf("proposals %d, applied %d; want the new line proposed and nothing written", proposals(l), lv.applied)
	}
}

func TestAcceptWithoutAProposal(t *testing.T) {
	env := AskEnv{Ledger: &memLedger{}, Levers: &askLevers{line: 633_000}}
	if _, err := AcceptCompaction(env, "", now); !errors.Is(err, ErrNoProposal) {
		t.Errorf("err = %v; want ErrNoProposal", err)
	}
}

// A proposal for a line that has since moved is not applied: what would
// be written is not what the developer said yes to.
func TestAcceptRefusesAMovedLine(t *testing.T) {
	l, lv := &memLedger{}, &askLevers{line: 633_000}
	env := AskEnv{Ledger: l, Shadow: &memSavings{}, Levers: lv, Graduated: func() bool { return true }}
	StepCompaction(askCfg, env, now)
	lv.line = 433_000
	if _, err := AcceptCompaction(env, "", now.Add(time.Hour)); err == nil || lv.applied != 0 {
		t.Errorf("err = %v, applied %d; want a refusal and nothing written", err, lv.applied)
	}
}

// Other rungs take no step: advise tells, autonomous writes through the
// coach's own changes.
func TestStepOnlyActsAtAsk(t *testing.T) {
	for _, rung := range []string{config.AutonomyOff, config.AutonomyAdvise, config.AutonomyAutonomous} {
		l, lv := &memLedger{}, &askLevers{line: 633_000}
		cfg := config.Config{Coach: config.CoachConfig{Autonomy: rung}}
		StepCompaction(cfg, AskEnv{Ledger: l, Shadow: &memSavings{}, Levers: lv, Graduated: func() bool { return true }}, now)
		if proposals(l) != 0 || lv.applied != 0 {
			t.Errorf("%s: proposals %d, applied %d; want nothing", rung, proposals(l), lv.applied)
		}
	}
}

// Raising context to ask proposes at once and writes nothing; lowering it
// from ask restores what an accepted line set.
func TestRaisingToAskProposesAndLoweringReverts(t *testing.T) {
	path := writeConfig(t, "coach:\n  autonomy: advise\n")
	l, lv := &memLedger{}, &askLevers{line: 633_000}
	if _, err := Apply(path, l, lv, now, func(c *config.Config) {
		c.Coach.Powers = map[string]string{config.PowerContext: config.AutonomyAsk}
	}); err != nil {
		t.Fatal(err)
	}
	if proposals(l) != 1 || lv.applied != 0 {
		t.Fatalf("raise to ask: proposals %d, applied %d; want one proposal, nothing written", proposals(l), lv.applied)
	}
	lv.written = lv.line
	if _, err := Apply(path, l, lv, now.Add(time.Hour), func(c *config.Config) {
		c.Coach.Powers[config.PowerContext] = config.AutonomyAdvise
	}); err != nil {
		t.Fatal(err)
	}
	if lv.written != 0 {
		t.Error("lowering from ask did not restore the setting")
	}
}

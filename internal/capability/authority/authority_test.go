package authority_test

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/authority"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/policy"
)

func cfg() config.Config { return config.Default() }

// The question an operator actually has is "what can TokenOps do
// without me". Answering it meant reading five settings in three files
// written in four vocabularies, and reconciling words that mean
// different things in different places — `active` most of all.
func TestEverySubsystemIsReportedInOneVocabulary(t *testing.T) {
	got := authority.Report(cfg())

	if len(got.Subsystems) < 4 {
		t.Fatalf("want every control surface reported, got %d: %+v",
			len(got.Subsystems), got.Subsystems)
	}
	seen := map[string]bool{}
	for _, s := range got.Subsystems {
		if s.Name == "" {
			t.Errorf("a subsystem has no name: %+v", s)
		}
		if seen[s.Name] {
			t.Errorf("%q reported twice", s.Name)
		}
		seen[s.Name] = true
		// The setting that produced the rung has to be named, or an
		// operator who wants to change it has to go hunting again.
		if s.Setting == "" {
			t.Errorf("%q does not say which setting produced it", s.Name)
		}
	}
}

// The daemon's mode caps daemon-side interventions only. The coach's
// powers act inside the client through hooks that never read the mode
// (ADR 0006, Decision 3), so reporting them capped would describe
// something that does not happen: a passive machine whose coach refuses
// re-reads must say so.
func TestTheDaemonCapsOnlyDaemonSideInterventions(t *testing.T) {
	c := cfg()
	c.Mode = config.ModePassive
	c.Coaching.Delivery = "intervene"

	for _, s := range authority.Report(c).Subsystems {
		switch s.Name {
		case "routing_approval":
			if s.Effective.MayAct() {
				t.Errorf("routing_approval may act under a passive daemon (effective %q)", s.Effective)
			}
		case "read_guard":
			if s.Effective != policy.Automatic {
				t.Errorf("read_guard effective %q under a passive daemon; the hook refuses re-reads regardless of mode", s.Effective)
			}
		}
	}
}

// A coach power configured above what it can deliver yet keeps both rungs
// visible and reports itself held back, so turning it up does not read as
// already done.
func TestUndeliverableRungsStayVisible(t *testing.T) {
	c := cfg()
	c.Coach.Powers = map[string]string{config.PowerWaste: config.AutonomyAsk}
	for _, s := range authority.Report(c).Subsystems {
		if s.Name != "read_guard" {
			continue
		}
		if s.Configured != policy.RequireApproval || s.Effective != policy.Recommend || !s.HeldBack() {
			t.Errorf("waste ask = configured %q effective %q held back %v", s.Configured, s.Effective, s.HeldBack())
		}
		return
	}
	t.Fatal("read_guard was not reported")
}

// An active daemon lets each subsystem's own setting decide.
func TestAnActiveDaemonDefersToEachSubsystem(t *testing.T) {
	c := cfg()
	c.Mode = config.ModeActive
	c.Coaching.Delivery = "advise"

	for _, s := range authority.Report(c).Subsystems {
		if s.Name != "coaching" {
			continue
		}
		if s.Effective != policy.Recommend {
			t.Errorf("effective = %q, want recommend", s.Effective)
		}
		if s.HeldBack() {
			t.Error("a subsystem at its own setting reports itself held back")
		}
		return
	}
	t.Fatal("coaching was not reported")
}

// The summary is what a status line prints. It must distinguish "acts
// on its own" from "only speaks" without the reader parsing a table.
func TestTheSummarySaysWhetherAnythingActs(t *testing.T) {
	passive := cfg()
	passive.Mode = config.ModePassive
	if authority.Report(passive).AnythingActs() {
		t.Error("a passive daemon reports that something acts")
	}

	active := cfg()
	active.Mode = config.ModeActive
	active.Coaching.Delivery = "intervene"
	if !authority.Report(active).AnythingActs() {
		t.Error("an intervening coach under an active daemon reports that nothing acts")
	}
}

// The read guard refuses redundant re-reads exactly when
// coaching.delivery is intervene. It used to be reported as observe-only
// and "derived from measured re-read history" while it was refusing reads
// on the machine running the report.
func TestReadGuardFollowsCoachingDelivery(t *testing.T) {
	for delivery, want := range map[string]policy.Authority{
		"intervene": policy.Automatic,
		"advise":    policy.Recommend,
		"observe":   policy.ObserveOnly,
	} {
		c := cfg()
		c.Mode = config.ModeActive
		c.Coaching.Delivery = delivery
		found := false
		for _, s := range authority.Report(c).Subsystems {
			if s.Name != "read_guard" {
				continue
			}
			found = true
			if s.Configured != want {
				t.Errorf("delivery %q: read_guard configured %q, want %q", delivery, s.Configured, want)
			}
			if !strings.Contains(s.Setting, "coaching.delivery") {
				t.Errorf("read_guard setting %q does not name coaching.delivery", s.Setting)
			}
		}
		if !found {
			t.Fatal("read_guard was not reported")
		}
	}
}

// Subsystems come back in a stable order so a status table does not
// reshuffle between refreshes.
func TestSubsystemsAreOrdered(t *testing.T) {
	first := authority.Report(cfg()).Subsystems
	for range 3 {
		got := authority.Report(cfg()).Subsystems
		for i := range got {
			if got[i].Name != first[i].Name {
				t.Fatalf("order changed: %v then %v", names(first), names(got))
			}
		}
	}
}

func names(ss []authority.Subsystem) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}

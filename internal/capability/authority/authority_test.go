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

// The daemon's authority caps every subsystem. This is the property
// four independent ladders could not offer, and the one an operator
// reaching for `mode: passive` in a hurry already believes they have.
func TestTheDaemonCapsEverySubsystem(t *testing.T) {
	c := cfg()
	c.Mode = config.ModePassive
	c.Coaching.Delivery = "intervene"
	c.Optimizer.SmartRouting.Enabled = true
	c.Optimizer.SmartRouting.Intervention = "auto"

	got := authority.Report(c)

	if got.Daemon != policy.ObserveOnly {
		t.Fatalf("daemon = %q, want observe_only", got.Daemon)
	}
	for _, s := range got.Subsystems {
		if s.Effective.MayAct() {
			t.Errorf("%q may act under a passive daemon (configured %q, effective %q)",
				s.Name, s.Configured, s.Effective)
		}
	}
}

// Capping must not hide what was configured. An operator needs to see
// that the coach is set to intervene and is being held back, or turning
// the daemon up will surprise them.
func TestCappingKeepsTheConfiguredRungVisible(t *testing.T) {
	c := cfg()
	c.Mode = config.ModePassive
	c.Coaching.Delivery = "intervene"

	for _, s := range authority.Report(c).Subsystems {
		if s.Name != "coaching" {
			continue
		}
		if s.Configured != policy.Automatic {
			t.Errorf("configured = %q, want automatic", s.Configured)
		}
		if s.Effective != policy.ObserveOnly {
			t.Errorf("effective = %q, want observe_only", s.Effective)
		}
		if !s.HeldBack() {
			t.Error("a capped subsystem does not report itself held back")
		}
		return
	}
	t.Fatal("coaching was not reported")
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
		"advise":    policy.ObserveOnly,
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

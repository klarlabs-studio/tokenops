package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// writeDeliveryConfig writes a minimal valid config with the given
// coaching delivery level and returns its path.
func writeDeliveryConfig(t *testing.T, delivery string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7878\nlog:\n  level: info\n  format: text\n"
	if delivery != "" {
		body += "coaching:\n  delivery: " + delivery + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// guardCmd builds a command carrying the --mode flag, optionally marked
// as explicitly set the way cobra marks it when a user types it.
func guardCmd(t *testing.T, explicit string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "read-guard"}
	var mode string
	cmd.Flags().StringVar(&mode, "mode", "", "")
	if explicit != "" {
		if err := cmd.Flags().Set("mode", explicit); err != nil {
			t.Fatalf("set --mode: %v", err)
		}
	}
	return cmd
}

func TestResolveGuardModeFollowsDelivery(t *testing.T) {
	for _, tc := range []struct {
		delivery string
		want     readguard.Mode
	}{
		{"intervene", readguard.ModeActive},
		{"advise", readguard.ModeObserve},
		{"observe", readguard.ModeObserve},
		// Default is advise, where the guard still only observes — so an
		// upgrade never starts blocking reads on its own.
		{"", readguard.ModeObserve},
	} {
		rf := &rootFlags{configPath: writeDeliveryConfig(t, tc.delivery)}
		if got := resolveGuardMode(guardCmd(t, ""), rf, ""); got != tc.want {
			t.Errorf("delivery %q: resolveGuardMode = %q, want %q", tc.delivery, got, tc.want)
		}
	}
}

// An explicit flag is the operator saying what they want on the command
// line. It beats config in both directions, including the direction that
// turns blocking ON against a passive config — pinning is how a hook gets
// promoted deliberately.
func TestResolveGuardModeExplicitFlagWins(t *testing.T) {
	rf := &rootFlags{configPath: writeDeliveryConfig(t, "observe")}
	if got := resolveGuardMode(guardCmd(t, "active"), rf, "active"); got != readguard.ModeActive {
		t.Errorf("explicit --mode=active under observe delivery = %q, want active", got)
	}
	rf = &rootFlags{configPath: writeDeliveryConfig(t, "intervene")}
	if got := resolveGuardMode(guardCmd(t, "observe"), rf, "observe"); got != readguard.ModeObserve {
		t.Errorf("explicit --mode=observe under intervene delivery = %q, want observe", got)
	}
}

// The guard sits in the agent's critical path. A config it cannot read
// must not make it start refusing reads — the whole package fails open.
func TestResolveGuardModeFailsOpenOnBadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("listen: [this is not valid yaml for listen\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rf := &rootFlags{configPath: path}
	if got := resolveGuardMode(guardCmd(t, ""), rf, ""); got != readguard.ModeObserve {
		t.Errorf("unreadable config resolved to %q, want observe (fail open)", got)
	}
}

func TestAdvisoryCoachingFollowsDelivery(t *testing.T) {
	for _, tc := range []struct {
		delivery string
		want     bool
	}{
		{"intervene", true},
		{"advise", true},
		{"observe", false},
		// The default nudges. Installing the Stop hook is the operator
		// asking for it; an upgrade must not quietly take it away.
		{"", true},
	} {
		rf := &rootFlags{configPath: writeDeliveryConfig(t, tc.delivery)}
		if got := advisoryCoaching(rf); got != tc.want {
			t.Errorf("delivery %q: advisoryCoaching = %v, want %v", tc.delivery, got, tc.want)
		}
	}
}

// A config that cannot be parsed must not silence the coach. Going quiet
// on a YAML error is the silent-failure shape this tool exists to find.
func TestAdvisoryCoachingFallsBackToTheDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("listen: [not valid\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if !advisoryCoaching(&rootFlags{configPath: path}) {
		t.Error("an unreadable config silenced the coach; it should fall back to the advise default")
	}
}

// The installed settings.json entry must not pin a mode by default.
// Pinning is what made coaching.delivery unreachable for read-guard: the
// flag always won, so the config key changed nothing until the hook was
// re-registered.
func TestReadGuardArgsOmitModeByDefault(t *testing.T) {
	got := readGuardArgs("")
	if len(got) != 1 || got[0] != "read-guard" {
		t.Errorf("readGuardArgs(\"\") = %v, want [read-guard] with no --mode", got)
	}
	pinned := readGuardArgs(readguard.ModeActive)
	if len(pinned) != 3 || pinned[1] != "--mode" || pinned[2] != "active" {
		t.Errorf("readGuardArgs(active) = %v, want it to pin --mode active", pinned)
	}
}

func TestSpecsForDoesNotPinGuardMode(t *testing.T) {
	for _, sp := range specsFor(false, true, 50) {
		for _, a := range sp.args {
			if a == "--mode" {
				t.Fatalf("specsFor pinned --mode in %v; config must govern the default", sp.args)
			}
		}
	}
}

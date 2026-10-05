package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/findings"
)

// Every visible command sits in a section, every hidden one still runs,
// and the help lists the sections in order with no command twice.
func TestHelpIsGrouped(t *testing.T) {
	root := NewRoot()
	for _, c := range root.Commands() {
		name := c.Name()
		if _, hidden := hiddenCommands[name]; hidden {
			if !c.Hidden {
				t.Errorf("%s should be hidden", name)
			}
			continue
		}
		if c.GroupID == "" {
			t.Errorf("%s is in no help section: add it to commandGroup and helpOrder, or to hiddenCommands", name)
		}
	}
	for name := range commandGroup {
		if c, _, err := root.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("commandGroup names %q, which is not a command", name)
		}
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	last := -1
	for _, g := range helpGroups {
		at := strings.Index(help, g.Title)
		if at < last {
			t.Errorf("section %q out of order", g.Title)
		}
		last = at
	}
	for name := range hiddenCommands {
		if strings.Contains(help, "\n  "+name+" ") {
			t.Errorf("hidden %s listed in the help", name)
		}
	}
	if strings.Contains(help, "--listen string") {
		t.Error("daemon overrides listed among the flags")
	}
	if strings.Index(help, "  glance ") > strings.Index(help, "  spend ") {
		t.Error("glance is not first")
	}
}

// dx answers the default week from the daemon's recent analysis, and
// reads transcripts when it is stale or --fresh asks.
func TestDXAnswersFromTheDaemonsAnalysis(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	snap := findings.SessionsSnapshot{ComputedAt: time.Now().Add(-90 * time.Minute)}
	snap.DX.Metrics.Prompts = 12
	snap.DX.Metrics.Sessions = 3
	if err := findings.WriteSnapshot(findings.DefaultDir(), snap); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		var out bytes.Buffer
		root := NewRoot()
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := run("dx"); !strings.Contains(got, "From the daemon's analysis 1h 30m ago") {
		t.Errorf("dx did not use the analysis:\n%s", got)
	}
	if got := run("dx", "--fresh"); strings.Contains(got, "daemon's analysis") {
		t.Errorf("--fresh used the analysis:\n%s", got)
	}
	if got := run("dx", "--days", "30"); strings.Contains(got, "daemon's analysis") {
		t.Errorf("another window used the week's analysis:\n%s", got)
	}
	snap.ComputedAt = time.Now().Add(-findings.MaxSnapshotAge - time.Minute)
	if err := findings.WriteSnapshot(findings.DefaultDir(), snap); err != nil {
		t.Fatal(err)
	}
	if got := run("dx"); strings.Contains(got, "daemon's analysis") {
		t.Errorf("a stale analysis was used:\n%s", got)
	}
}

// A subcommand never carries a top-level command's description: a
// `status` under `daemon` is not `tokenops status`.
func TestSubcommandsKeepTheirOwnDescriptions(t *testing.T) {
	root := NewRoot()
	top := map[string]string{}
	for _, c := range root.Commands() {
		top[c.Short] = c.Name()
	}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if owner, ok := top[sub.Short]; ok {
				t.Errorf("%s has %s's description %q", sub.CommandPath(), owner, sub.Short)
			}
			walk(sub)
		}
	}
	for _, c := range root.Commands() {
		walk(c)
	}
}

// coach stats shows both ledgers; explain takes a term, and names a
// missing term or decision as either.
func TestCoachStatsAndExplain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		root := NewRoot()
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), err
	}
	out, err := run("coach", "stats")
	if err != nil || !strings.Contains(out, "No coach-hook activity yet") || !strings.Contains(out, "No read-guard activity yet") {
		t.Errorf("coach stats: %v\n%s", err, out)
	}
	if out, err := run("coach", "stats", "--json"); err != nil || !strings.Contains(out, `"read_guard"`) || !strings.Contains(out, `"budget"`) {
		t.Errorf("coach stats --json: %v\n%s", err, out)
	}
	if out, err := run("explain", "wall-clock"); err != nil || !strings.Contains(out, "wall-clock") {
		t.Errorf("explain term: %v\n%s", err, out)
	}
	// A mistyped term gets suggestions even with no event store; a
	// decision's ID needs the store, and says so.
	if _, err := run("explain", "wall-clok"); err == nil || !strings.Contains(err.Error(), "no term or decision") {
		t.Errorf("explain typo: %v", err)
	}
	if _, err := run("explain", "decision:missing"); err == nil || !strings.Contains(err.Error(), "event store") {
		t.Errorf("explain decision without a store: %v", err)
	}
	if c, _, err := NewRoot().Find([]string{"decision"}); err == nil && c.Name() == "decision" {
		t.Error("decision still exists; explain answers decisions")
	}
}

package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/checkup"
)

// The report leads with what was spent, says the money is a measure on a
// flat plan, and gives every finding its fix; an empty week says where it
// looked.
func TestRenderCheckup(t *testing.T) {
	var b bytes.Buffer
	u := checkup.Usage{Harness: "Claude Code", Model: "claude-opus-5-5", Turns: 12, InputTokens: 5_400_000_000, OutputTokens: 1, CostUSD: 1609.92}
	renderCheckup(&b, checkup.Report{
		Window: "last 7d", Usage: []checkup.Usage{u, {Harness: "Codex", Model: "codex-auto-review", Turns: 3, UnpricedTurns: 3}},
		Total:    checkup.Usage{Turns: 15, InputTokens: 5_400_000_000, CostUSD: 1609.92, UnpricedTurns: 3},
		Findings: []checkup.Finding{{Level: checkup.LevelNotice, Title: "Agents re-read 10 unchanged files", Evidence: "11 re-reads", Fix: "tokenops hooks install --read-guard"}},
	}, false)
	out := b.String()
	for _, want := range []string{"last 7d", "claude-opus-5-5", "5.40B", "$1609.92", "unpriced", "$1609.92+", "a measure, not a bill",
		"● Agents re-read 10 unchanged files", "fix: tokenops hooks install --read-guard"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	b.Reset()
	renderCheckup(&b, checkup.Report{Window: "last 7d"}, false)
	if !strings.Contains(b.String(), "No agent turns found") {
		t.Errorf("empty week:\n%s", b.String())
	}
}

// A machine the coach is set up on is not told to set it up.
func TestCheckupSuggestsInitOnlyWithoutTheCoach(t *testing.T) {
	r := checkup.Report{Window: "last 7d", Usage: []checkup.Usage{{Harness: "Claude Code", Model: "m", Turns: 1}}, Total: checkup.Usage{Turns: 1}}
	var b bytes.Buffer
	renderCheckup(&b, r, false)
	if !strings.Contains(b.String(), "`tokenops init` sets up the coach") {
		t.Errorf("without the coach:\n%s", b.String())
	}
	b.Reset()
	renderCheckup(&b, r, true)
	if strings.Contains(b.String(), "tokenops init") {
		t.Errorf("with the coach installed:\n%s", b.String())
	}
}

// The checkup runs before anything is set up: with no config and no store
// the catalog-drift finding is left out and nothing fails.
func TestWithDriftSourcesNeedsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("TOKENOPS_STORAGE_PATH", filepath.Join(home, "missing.db"))
	var o checkup.Options
	closeStore := withDriftSources(context.Background(), &rootFlags{}, &o)
	defer closeStore()
	if o.Events != nil || o.Price != nil {
		t.Errorf("drift sources without a config or store: %+v", o)
	}
}

package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/money"
)

// The installed-product end-to-end test found this: `tokenops init`
// writes storage, registers the MCP server and installs the Claude Code
// hooks — and enables no ingestion source at all. A fresh install
// therefore ingests nothing, and nothing in the wiring summary says so.
//
// It is the silent-success shape sitting in the first command of the
// journey. The operator finishes init, reads a mostly-green report, and
// has a TokenOps that will never see a single event.
func TestSetupAsksForAnIngestionSourceWhenNoneIsEnabled(t *testing.T) {
	path := seedConfig(t)
	stubLocalSources(t)

	step := ingestionStep(path)

	if !step.Manual {
		t.Error("a config with no ingestion source reported nothing outstanding")
	}
	if !strings.Contains(step.Detail, "vendor-usage enable") {
		t.Errorf("the step does not name the command that fixes it: %q", step.Detail)
	}
	if step.Name == "" {
		t.Error("the step has no name")
	}
}

// Once a source is enabled the step stops asking. A summary that asks
// for work already done is one an operator stops reading — the same
// reasoning the daemon-unit step is built on.
func TestSetupStopsAskingOnceASourceIsEnabled(t *testing.T) {
	path := seedConfig(t)
	stubLocalSources(t)
	cfg, err := readMutableConfig(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true
	if err := writeMutableConfig(path, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}

	step := ingestionStep(path)
	if step.Manual {
		t.Errorf("a configured source still reported as outstanding: %q", step.Detail)
	}
	if !strings.Contains(step.Detail, "claude_code_jsonl") {
		t.Errorf("the step does not name what is ingesting: %q", step.Detail)
	}
}

// An unreadable config reports as outstanding rather than as fine.
// Claiming ingestion is configured when nothing checked costs exactly
// the gap this step exists to close.
func TestSetupAsksWhenTheConfigCannotBeRead(t *testing.T) {
	step := ingestionStep("/nonexistent/config.yaml")
	if !step.Manual && step.Err == nil {
		t.Errorf("an unreadable config reported ingestion as configured: %+v", step)
	}
}

func stubLocalSources(t *testing.T, present ...string) {
	t.Helper()
	prev := localTranscriptSources
	localTranscriptSources = func() []string { return present }
	t.Cleanup(func() { localTranscriptSources = prev })
}

// Autonomous by default: on a fresh install the readers whose sessions are
// on this machine are turned on, and the step says how to stop one.
func TestSetupTurnsOnLocalReadersWhoseDataIsPresent(t *testing.T) {
	path := seedConfig(t)
	stubLocalSources(t, "claude-code-jsonl", "opencode")

	step := ingestionStep(path)
	if step.Manual || step.Err != nil {
		t.Fatalf("step %+v; want the readers turned on", step)
	}
	if !strings.Contains(step.Detail, "turned on claude-code-jsonl, opencode") || !strings.Contains(step.Detail, "--disable") {
		t.Errorf("detail %q", step.Detail)
	}
	cfg, err := readMutableConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.VendorUsage.ClaudeCodeJSONL.Enabled || !cfg.VendorUsage.OpenCode.Enabled || cfg.VendorUsage.CodexJSONL.Enabled {
		t.Errorf("enabled: claude %v opencode %v codex %v", cfg.VendorUsage.ClaudeCodeJSONL.Enabled, cfg.VendorUsage.OpenCode.Enabled, cfg.VendorUsage.CodexJSONL.Enabled)
	}
}

// A reader the operator switched off stays off; init only names it.
func TestSetupDoesNotOverrideAReaderLeftOff(t *testing.T) {
	path := seedConfig(t)
	cfg, err := readMutableConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true
	if err := writeMutableConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	stubLocalSources(t, "claude-code-jsonl", "codex-jsonl")

	step := ingestionStep(path)
	if !strings.Contains(step.Detail, "sessions found but not read: codex-jsonl") {
		t.Errorf("detail %q", step.Detail)
	}
	if cfg, _ := readMutableConfig(path); cfg.VendorUsage.CodexJSONL.Enabled {
		t.Error("init turned on a reader the operator had left off")
	}
}

// --no-wire means "config only", and turning readers on is config: an
// end-to-end run found a --no-wire install that ingested nothing.
func TestInitNoWireStillTurnsOnLocalReaders(t *testing.T) {
	stubLocalSources(t, "claude-code-jsonl")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs([]string{"init", "--config", cfgPath, "--storage-path", filepath.Join(dir, "events.db"), "--no-wire", "--currency", "EUR"})
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "ingestion: turned on claude-code-jsonl") {
		t.Errorf("output: %s", out.String())
	}
	cfg, err := readMutableConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.VendorUsage.ClaudeCodeJSONL.Enabled {
		t.Error("the reader was not turned on")
	}
}

// A plan bound minutes into the window costs cents; a ratio over it read
// as a 138x return in an end-to-end run.
func TestValuePerPlanNeedsAPlanCostToCompareWith(t *testing.T) {
	now := time.Now().UTC()
	v := &spendView{}
	v.Summary.APIEquivalentUSD = 0.52
	fillPlanCost(v, map[string]string{"ratio-test": "claude-max-20x"}, "USD", money.Rate{}, false, now.Add(-time.Minute), now)
	if v.PlanCost >= minPlanCostForRatio || v.ValuePerPlanUnit != 0 {
		t.Errorf("plan cost %.4f, ratio %.1f; want no ratio below one unit", v.PlanCost, v.ValuePerPlanUnit)
	}
	v = &spendView{}
	v.Summary.APIEquivalentUSD = 400
	fillPlanCost(v, map[string]string{"ratio-test": "claude-max-20x"}, "USD", money.Rate{}, false, now.AddDate(0, 0, -30), now)
	if v.ValuePerPlanUnit <= 1 {
		t.Errorf("a month of Max 20x against $400 of usage: ratio %.1f", v.ValuePerPlanUnit)
	}
}

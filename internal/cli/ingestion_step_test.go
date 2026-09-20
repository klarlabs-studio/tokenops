package cli

import (
	"strings"
	"testing"
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

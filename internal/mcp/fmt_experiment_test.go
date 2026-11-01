package mcp

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	mcpserver "go.klarlabs.de/mcp/server"

	"go.klarlabs.de/tokenops/internal/config"
)

// A share fmt cannot run is the agent's to correct before anyone is asked.
func TestAnInvalidFmtShareIsRefusedBeforeAsking(t *testing.T) {
	for _, share := range []string{"80", "half", ""} {
		srv, _ := spyServer(t)
		op := accepted(true)
		err := execPublic(withOperator(op), t, srv, "tokenops_experiment", map[string]any{"action": "fmt", "fmt_share": share})
		var ie *mcpserver.ToolInputError
		if !errors.As(err, &ie) || !strings.Contains(ie.Message, "experiment must be off, on, or a percent") || len(op.messages) != 0 {
			t.Errorf("share %q: err = %v, asked %d times", share, err, len(op.messages))
		}
	}
}

// Once the operator says yes, the share is written where fmt reads it.
func TestSetFmtExperimentWritesTheConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.WriteMutable(path, config.Default()); err != nil {
		t.Fatal(err)
	}
	res, err := setFmtExperiment(path, "on")
	if err != nil {
		t.Fatal(err)
	}
	if res.Fmt == nil || res.Fmt.RawPercent != 50 || !strings.HasPrefix(res.Fmt.Note, "on: 50%") {
		t.Errorf("result: %+v", res.Fmt)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil || cfg.Optimizer.CommandFmt.Experiment != 50 {
		t.Fatalf("config: %d, %v", cfg.Optimizer.CommandFmt.Experiment, err)
	}
	if res, err := setFmtExperiment(path, "off"); err != nil || res.Fmt.RawPercent != 0 {
		t.Fatalf("off: %+v, %v", res, err)
	}
}

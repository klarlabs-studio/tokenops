package mcp

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

func newControlServer(t *testing.T, d ControlDeps) *Server {
	t.Helper()
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterControlTools(srv, d); err != nil {
		t.Fatalf("RegisterControlTools: %v", err)
	}
	return srv
}

func TestControlToolsAttached(t *testing.T) {
	srv := newControlServer(t, ControlDeps{})
	for _, want := range []string{"tokenops_version", "tokenops_status", "tokenops_config"} {
		if _, ok := srv.GetTool(want); !ok {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestVersionToolReturnsFields(t *testing.T) {
	srv := newControlServer(t, ControlDeps{})
	out := execTool(t, srv, "tokenops_version", nil)
	for _, want := range []string{`"version"`, `"commit"`, `"display"`, `"schema_version"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output: %s", want, out)
		}
	}
}

func TestStatusToolReportsReadiness(t *testing.T) {
	srv := newControlServer(t, ControlDeps{ReadyCheck: func() bool { return true }})
	out := execTool(t, srv, "tokenops_status", nil)
	if !strings.Contains(out, `"ready": true`) {
		t.Errorf("expected ready=true in output: %s", out)
	}
	if !strings.Contains(out, `"state": "ready"`) {
		t.Errorf("expected state=ready: %s", out)
	}
}

func TestStatusToolReportsNotReady(t *testing.T) {
	srv := newControlServer(t, ControlDeps{ReadyCheck: func() bool { return false }})
	out := execTool(t, srv, "tokenops_status", nil)
	if !strings.Contains(out, `"ready": false`) {
		t.Errorf("expected ready=false: %s", out)
	}
}

func TestConfigToolReturnsSnapshot(t *testing.T) {
	cfg := json.RawMessage(`{"listen":"127.0.0.1:7878"}`)
	srv := newControlServer(t, ControlDeps{ConfigJSON: cfg})
	out := execTool(t, srv, "tokenops_config", nil)
	if !strings.Contains(out, "127.0.0.1") {
		t.Errorf("expected config in output: %s", out)
	}
}

func TestConfigToolReportsMissing(t *testing.T) {
	srv := newControlServer(t, ControlDeps{})
	out := execTool(t, srv, "tokenops_config", nil)
	if !strings.Contains(out, "snapshot not available") {
		t.Errorf("expected missing-snapshot marker: %s", out)
	}
}

func TestStatusToolReportsBlockersAndNextActions(t *testing.T) {
	// Fresh-install scenario: storage/rules off, no providers. Caller
	// should see three blockers plus the actionable init hint.
	cfg := config.Default()
	srv := newControlServer(t, ControlDeps{
		Config:     &cfg,
		ReadyCheck: func() bool { return false },
	})
	out := execTool(t, srv, "tokenops_status", nil)

	for _, want := range []string{
		`"storage_disabled"`,
		`"rules_disabled"`,
		`"providers_unconfigured"`,
		"run `tokenops init`",
		`"state": "not_configured"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output: %s", want, out)
		}
	}
}

func TestStatusToolReportsDegradedWhenReadyButBlockersExist(t *testing.T) {
	// MCP `serve` opens its own sqlite store and is therefore ready,
	// even if the on-disk config still has storage/rules disabled.
	// `degraded` keeps the distinction between "broken" and "running
	// with reduced surface area" legible to callers.
	cfg := config.Default()
	srv := newControlServer(t, ControlDeps{
		Config:     &cfg,
		ReadyCheck: func() bool { return true },
	})
	out := execTool(t, srv, "tokenops_status", nil)
	if !strings.Contains(out, `"state": "degraded"`) {
		t.Errorf("expected state=degraded when ready with blockers: %s", out)
	}
	if !strings.Contains(out, `"ready": true`) {
		t.Errorf("expected ready=true: %s", out)
	}
}

func TestStatusToolReportsReadyWhenAllConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.Enabled = true
	cfg.Rules.Enabled = true
	cfg.Providers = map[string]string{"anthropic": "https://api.anthropic.com"}
	srv := newControlServer(t, ControlDeps{
		Config:     &cfg,
		ReadyCheck: func() bool { return true },
	})
	out := execTool(t, srv, "tokenops_status", nil)
	if !strings.Contains(out, `"state": "ready"`) {
		t.Errorf("expected state=ready: %s", out)
	}
	if !strings.Contains(out, `"blockers": []`) {
		t.Errorf("expected empty blockers array: %s", out)
	}
}

// A stale enabled vendor-usage source must surface as a soft `warnings`
// entry with a matching next_action, and downgrade an otherwise-ready
// state to `degraded` while keeping ready:true — never a blocker.
func TestStatusToolSurfacesStaleIngestionWarnings(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.Enabled = true
	cfg.Rules.Enabled = true
	cfg.Providers = map[string]string{"anthropic": "https://api.anthropic.com"}
	srv := newControlServer(t, ControlDeps{
		Config:     &cfg,
		ReadyCheck: func() bool { return true },
		StaleSources: func() []config.StaleSource {
			return []config.StaleSource{
				{Name: "claude_code_jsonl", SourceTag: "claude-code-jsonl", WindowHours: 48},
			}
		},
	})
	out := execTool(t, srv, "tokenops_status", nil)

	for _, want := range []string{
		`"warnings"`,
		"ingestion stale: claude-code-jsonl has 0 events in the last 48h",
		config.StaleIngestionNextAction,
		`"ready": true`,
		`"state": "degraded"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output: %s", want, out)
		}
	}
	// Stale sources are a soft signal, not a config blocker.
	if !strings.Contains(out, `"blockers": []`) {
		t.Errorf("stale ingestion must not appear as a blocker: %s", out)
	}
}

// No stale sources → payload is unchanged: no `warnings` key, state
// stays ready.
func TestStatusToolNoWarningsWhenIngestionFresh(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.Enabled = true
	cfg.Rules.Enabled = true
	cfg.Providers = map[string]string{"anthropic": "https://api.anthropic.com"}
	srv := newControlServer(t, ControlDeps{
		Config:       &cfg,
		ReadyCheck:   func() bool { return true },
		StaleSources: func() []config.StaleSource { return nil },
	})
	out := execTool(t, srv, "tokenops_status", nil)
	if strings.Contains(out, `"warnings"`) {
		t.Errorf("did not expect warnings key when ingestion is fresh: %s", out)
	}
	if !strings.Contains(out, `"state": "ready"`) {
		t.Errorf("expected state=ready with no warnings: %s", out)
	}
}

// A nil StaleSources hook (no store wired) must not panic and must not
// add a warnings key.
func TestStatusToolNilStaleSourcesHook(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.Enabled = true
	cfg.Rules.Enabled = true
	cfg.Providers = map[string]string{"anthropic": "https://api.anthropic.com"}
	srv := newControlServer(t, ControlDeps{
		Config:     &cfg,
		ReadyCheck: func() bool { return true },
	})
	out := execTool(t, srv, "tokenops_status", nil)
	if strings.Contains(out, `"warnings"`) {
		t.Errorf("nil hook should not add warnings: %s", out)
	}
	if !strings.Contains(out, `"state": "ready"`) {
		t.Errorf("expected state=ready: %s", out)
	}
}

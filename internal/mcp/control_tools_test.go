package mcp

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
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

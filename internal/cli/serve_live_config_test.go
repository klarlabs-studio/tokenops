package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/mcp"
)

// serve lives as long as its MCP client. When an agent calls
// tokenops_plan_set or tokenops_vendor_usage_setup, config.yaml changes under
// a running server; the watcher picks that up, but tokenops_config, status
// blockers, the stale-sources check and vendor-usage status were all wired to
// the snapshot taken at startup and kept answering with the old config until
// the client restarted. These drive serve's own wiring with a config that
// changes after the deps are built.

type liveConfigFixture struct{ cur config.Config }

func (f *liveConfigFixture) get() *config.Config { c := f.cur; return &c }

func serveTestServer(t *testing.T, d mcp.ControlDeps, g mcp.GapDeps) *mcp.Server {
	t.Helper()
	// The daemon probe goes to the real URL hint and port; nothing in a test
	// may touch the operator's running daemon.
	d.DaemonProbe = nil
	srv := mcp.NewServer("tokenops", "test", nil)
	if err := mcp.RegisterControlTools(srv, d); err != nil {
		t.Fatal(err)
	}
	if err := mcp.RegisterGapTools(srv, g); err != nil {
		t.Fatal(err)
	}
	return srv
}

func callServeTool(t *testing.T, srv *mcp.Server, name string) string {
	t.Helper()
	tool, ok := srv.GetTool(name)
	if !ok {
		t.Fatalf("no tool %q", name)
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if s, ok := out.(string); ok {
		return s
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestServeControlToolsReadConfigWrittenAfterStart(t *testing.T) {
	f := &liveConfigFixture{cur: config.Default()}
	srv := serveTestServer(t,
		serveControlDeps(f.get, func() bool { return true }, nil, nil),
		serveGapDeps(f.get, nil))

	f.cur.Listen = "127.0.0.1:9999"
	f.cur.Storage.Enabled = true
	f.cur.Rules.Enabled = true
	f.cur.Providers = map[string]string{"anthropic": "https://api.anthropic.com"}
	f.cur.VendorUsage.ClaudeUsageMeter.SessionKey = "sk-ant-sid01-secret"

	cfgOut := callServeTool(t, srv, "tokenops_config")
	if !strings.Contains(cfgOut, "127.0.0.1:9999") {
		t.Errorf("tokenops_config serves the startup snapshot: %s", cfgOut)
	}
	if strings.Contains(cfgOut, "sk-ant-sid01-secret") {
		t.Fatalf("tokenops_config leaked the session key: %s", cfgOut)
	}

	status := callServeTool(t, srv, "tokenops_status")
	if strings.Contains(status, "storage_disabled") || !strings.Contains(status, `"state":"ready"`) {
		t.Errorf("status blockers come from the startup snapshot: %s", status)
	}
}

func TestServeVendorUsageStatusReadsConfigWrittenAfterStart(t *testing.T) {
	f := &liveConfigFixture{cur: config.Default()}
	counts := func(context.Context, time.Time, time.Time) (map[string]int64, error) {
		return map[string]int64{}, nil
	}
	srv := serveTestServer(t, mcp.ControlDeps{}, serveGapDeps(f.get, counts))

	f.cur.VendorUsage.ClaudeUsageMeter.Enabled = true
	out := callServeTool(t, srv, "tokenops_vendor_usage_status")
	if !strings.Contains(out, `"source_tag":"claude-usage-meter","enabled":true`) {
		t.Errorf("vendor-usage status serves the startup snapshot: %s", out)
	}
}

// A config that failed to load must not look like an empty one: the tool
// stays disabled rather than listing every source as off.
func TestServeGapDepsWithoutConfigDisablesVendorUsage(t *testing.T) {
	counts := func(context.Context, time.Time, time.Time) (map[string]int64, error) { return nil, nil }
	srv := serveTestServer(t, mcp.ControlDeps{}, serveGapDeps(nil, counts))
	if out := callServeTool(t, srv, "tokenops_vendor_usage_status"); !strings.Contains(out, "storage_disabled") {
		t.Errorf("want the disabled marker, got %s", out)
	}
}

type emptyCounter struct{}

func (emptyCounter) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{}, nil
}

// Enabling a source through an MCP tool and then asking status is exactly
// when a silent source matters; the check has to see the source.
func TestServeStaleSourcesCheckReadsConfigWrittenAfterStart(t *testing.T) {
	f := &liveConfigFixture{cur: config.Default()}
	check := staleSourcesCheck(context.Background(), emptyCounter{}, f.get)
	if check == nil {
		t.Fatal("no check built")
	}
	if got := check(); len(got) != 0 {
		t.Fatalf("nothing enabled yet, got %+v", got)
	}

	f.cur.VendorUsage.ClaudeUsageMeter.Enabled = true
	got := check()
	if len(got) != 1 || got[0].SourceTag != "claude-usage-meter" {
		t.Errorf("source enabled after start not checked: %+v", got)
	}
}

func TestServeStaleSourcesCheckNilWithoutConfig(t *testing.T) {
	if check := staleSourcesCheck(context.Background(), emptyCounter{}, nil); check != nil {
		t.Error("check built without a config")
	}
}

// serve used to leave the daemon hooks unwired, so tokenops_domain_events
// answered zeros for counters that only exist in the daemon. The status and
// version tools need the same daemon to report its version.
func TestServeControlDepsWireTheDaemonAndDriftHooks(t *testing.T) {
	drift := func() mcp.BinaryDrift { return mcp.BinaryDrift{OutOfDate: true} }
	d := serveControlDeps(nil, nil, nil, drift)
	if d.DaemonProbe == nil || d.DaemonVersion == nil || d.DaemonDomainEvents == nil {
		t.Errorf("daemon hooks not wired: probe=%t version=%t events=%t",
			d.DaemonProbe != nil, d.DaemonVersion != nil, d.DaemonDomainEvents != nil)
	}
	if d.BinaryDrift == nil || !d.BinaryDrift().OutOfDate {
		t.Error("binary drift check not wired")
	}
}

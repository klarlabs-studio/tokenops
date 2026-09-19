package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// serve outlives config writes: an agent binds a plan or switches a vendor
// source on through an MCP tool, which rewrites config.yaml while the server
// keeps running. Tools that read a startup snapshot kept reporting the
// pre-write state until the client restarted. These pin that the control and
// gap tools read the getter at call time.

func TestConfigToolReadsLiveConfigAndRedactsIt(t *testing.T) {
	cur := config.Default()
	srv := newControlServer(t, ControlDeps{
		ConfigJSON:   json.RawMessage(`{"listen":"stale-snapshot"}`),
		ConfigGetter: func() *config.Config { c := cur; return &c },
	})

	cur.Listen = "127.0.0.1:9999"
	cur.VendorUsage.ClaudeUsageMeter.Enabled = true
	cur.VendorUsage.ClaudeUsageMeter.SessionKey = "sk-ant-sid01-secret"
	out := execTool(t, srv, "tokenops_config", nil)

	if !strings.Contains(out, "127.0.0.1:9999") {
		t.Errorf("config written after start not reported: %s", out)
	}
	if strings.Contains(out, "stale-snapshot") {
		t.Errorf("startup snapshot served over the live config: %s", out)
	}
	if strings.Contains(out, "sk-ant-sid01-secret") {
		t.Fatalf("session key leaked unredacted: %s", out)
	}
	if !strings.Contains(out, config.SensitiveHeaderPlaceholder) {
		t.Errorf("expected the redaction placeholder: %s", out)
	}
}

func TestStatusBlockersFollowLiveConfig(t *testing.T) {
	cur := config.Default()
	stale := config.Default() // storage/rules off, no providers: three blockers
	d := ControlDeps{
		Config:       &stale,
		ConfigGetter: func() *config.Config { c := cur; return &c },
		ReadyCheck:   func() bool { return true },
	}

	cur.Storage.Enabled = true
	cur.Rules.Enabled = true
	cur.Providers = map[string]string{"anthropic": "https://api.anthropic.com"}
	res := statusInfo(d)

	if len(res.Blockers) != 0 || res.State != "ready" {
		t.Errorf("blockers come from the startup snapshot, not the live config: %+v", res)
	}
}

// Without a getter the static fields still work, so a caller that never
// wires a watcher keeps its old behaviour.
func TestControlToolsFallBackToStaticConfig(t *testing.T) {
	cfg := config.Default()
	res := statusInfo(ControlDeps{Config: &cfg, ReadyCheck: func() bool { return true }})
	if len(res.Blockers) == 0 {
		t.Errorf("static config ignored without a getter: %+v", res)
	}
}

func TestVendorUsageStatusFollowsLiveConfig(t *testing.T) {
	cur := config.Default()
	stale := config.Default()
	srv := NewServer("tokenops", "test", nil)
	if err := RegisterGapTools(srv, GapDeps{
		Config:       &stale,
		ConfigGetter: func() *config.Config { c := cur; return &c },
		Counts: func(context.Context, time.Time, time.Time) (map[string]int64, error) {
			return map[string]int64{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	// What tokenops_vendor_usage_setup does: switch a source on in config.yaml.
	cur.VendorUsage.ClaudeUsageMeter.Enabled = true
	out := execTool(t, srv, "tokenops_vendor_usage_status", nil)

	var res vendorUsageResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	for _, s := range res.Sources {
		if s.SourceTag == "claude-usage-meter" {
			if !s.Enabled {
				t.Errorf("source enabled after start still reported off: %+v", s)
			}
			return
		}
	}
	t.Fatalf("claude-usage-meter missing: %s", out)
}

// A nil getter result is "no config loaded", which disables the tool rather
// than reporting an empty source list.
func TestVendorUsageStatusDisabledWithoutConfig(t *testing.T) {
	srv := NewServer("tokenops", "test", nil)
	if err := RegisterGapTools(srv, GapDeps{
		ConfigGetter: func() *config.Config { return nil },
		Counts: func(context.Context, time.Time, time.Time) (map[string]int64, error) {
			return nil, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_vendor_usage_status", nil)
	if !strings.Contains(out, "storage_disabled") {
		t.Errorf("want the disabled marker, got %s", out)
	}
}

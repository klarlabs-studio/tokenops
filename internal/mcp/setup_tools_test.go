package mcp

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

type setupFixture struct {
	t       *testing.T
	path    string
	applied int
	env     map[string]string
	baseURL string
}

func newSetupFixture(t *testing.T, initial string) *setupFixture {
	t.Helper()
	f := &setupFixture{t: t, path: filepath.Join(t.TempDir(), "config.yaml"), env: map[string]string{}}
	if initial == "" {
		// As `tokenops init` leaves it: the tools edit a config, not create one.
		initial = "log:\n  level: info\n"
	}
	if err := os.WriteFile(f.path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *setupFixture) server() *Server {
	f.t.Helper()
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	deps := SetupDeps{
		ConfigPath:   f.path,
		ApplyConfig:  func() string { f.applied++; return "restarted the daemon; the change is live" },
		Getenv:       func(k string) string { return f.env[k] },
		MeterBaseURL: f.baseURL,
	}
	if err := RegisterSetupTools(srv, deps); err != nil {
		f.t.Fatal(err)
	}
	return srv
}

func (f *setupFixture) config() config.Config {
	f.t.Helper()
	cfg, err := config.ReadMutable(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	return cfg
}

func TestPlanSetToolBindsAndMakesItLive(t *testing.T) {
	f := newSetupFixture(t, "")
	got := callTool(t, f.server(), "tokenops_plan_set", map[string]any{
		"provider": "anthropic", "plan": "claude-enterprise", "spend_limit_usd": 1500,
	})
	if got["plan"] != "claude-enterprise" {
		t.Fatalf("response = %v", got)
	}
	cfg := f.config()
	if cfg.Plans["anthropic"] != "claude-enterprise" || cfg.PlanLimits["anthropic"].SpendLimitUSD != 1500 {
		t.Errorf("config plans=%v limits=%v", cfg.Plans, cfg.PlanLimits)
	}
	if f.applied != 1 || !strings.Contains(got["note"].(string), "live") {
		t.Errorf("change not applied: applied=%d note=%v", f.applied, got["note"])
	}
}

// The same rule as `plan set`: a spend-billed plan with no limit to measure
// against is refused, unless the usage meter will report it.
func TestPlanSetToolRefusesAnEnterprisePlanWithNoLimit(t *testing.T) {
	f := newSetupFixture(t, "")
	if err := execToolErr(t, f.server(), "tokenops_plan_set", map[string]any{
		"provider": "anthropic", "plan": "claude-enterprise",
	}); err == nil {
		t.Fatal("bound claude-enterprise with neither a limit nor the meter")
	}
	if f.applied != 0 {
		t.Error("restarted the daemon for a refused binding")
	}

	metered := newSetupFixture(t, "vendor_usage:\n  claude_usage_meter:\n    enabled: true\n")
	callTool(t, metered.server(), "tokenops_plan_set", map[string]any{"provider": "anthropic", "plan": "claude-enterprise"})
	if metered.config().Plans["anthropic"] != "claude-enterprise" {
		t.Error("refused a binding the usage meter supplies the limit for")
	}
}

func TestPlanSetToolListsAndClears(t *testing.T) {
	f := newSetupFixture(t, "plans:\n  anthropic: claude-max-20x\n")
	srv := f.server()
	listed := callTool(t, srv, "tokenops_plan_set", map[string]any{})
	if plans, _ := listed["plans"].(map[string]any); plans["anthropic"] != "claude-max-20x" {
		t.Errorf("list = %v", listed)
	}
	callTool(t, srv, "tokenops_plan_set", map[string]any{"provider": "anthropic", "clear": true})
	if _, still := f.config().Plans["anthropic"]; still {
		t.Error("clear left the binding in place")
	}
}

const enterpriseMeterUsage = `{"five_hour": null, "seven_day": null, "seven_day_opus": null,
  "extra_usage": {"is_enabled": true, "monthly_limit": 150000, "used_credits": 109563,
    "utilization": 73.042, "currency": "USD", "decimal_places": 2, "spend_limit_reached": false}}`

func meterStub(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/organizations":
			_, _ = w.Write([]byte(`[{"uuid":"personal","name":"Personal"},{"uuid":"work","name":"Work"}]`))
		case "/api/organizations/personal/usage":
			_, _ = w.Write([]byte(`{"five_hour": null, "seven_day": null, "extra_usage": null}`))
		case "/api/organizations/work/usage":
			_, _ = w.Write([]byte(enterpriseMeterUsage))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The session key logs in as the user. The tool has no argument to pass it
// in, and without it in the environment or config it says how to provide
// it — never "paste it here".
func TestMeterSetupToolNeverTakesTheKeyThroughTheChat(t *testing.T) {
	f := newSetupFixture(t, "")
	got := callTool(t, f.server(), "tokenops_vendor_usage_setup", map[string]any{})
	if got["error"] != "session_key_missing" {
		t.Fatalf("response = %v", got)
	}
	if hint, _ := got["hint"].(string); !strings.Contains(hint, "must not go through this chat") {
		t.Errorf("hint does not keep the key out of the chat: %q", hint)
	}
	if f.config().VendorUsage.ClaudeUsageMeter.Enabled {
		t.Error("enabled the meter with no key")
	}
}

func TestMeterSetupToolConnectsTheOrganizationThatReportsUsage(t *testing.T) {
	f := newSetupFixture(t, "")
	f.env[meterKeyEnv] = "sk-ant-sid-test-secret"
	f.baseURL = meterStub(t, http.StatusOK)
	raw := execTool(t, f.server(), "tokenops_vendor_usage_setup", map[string]any{})
	if strings.Contains(raw, "sk-ant-sid-test-secret") {
		t.Fatalf("the session key was echoed back into the transcript: %s", raw)
	}
	got := callTool(t, f.server(), "tokenops_vendor_usage_setup", map[string]any{})
	if got["organization"] != "Work" {
		t.Errorf("organization = %v, want the one reporting usage", got["organization"])
	}
	if reports, _ := got["reports"].([]any); len(reports) != 1 || !strings.Contains(reports[0].(string), "1095.63 of 1500.00 USD") {
		t.Errorf("reports = %v", got["reports"])
	}
	m := f.config().VendorUsage.ClaudeUsageMeter
	if !m.Enabled || m.OrgID != "work" || m.SessionKey != "sk-ant-sid-test-secret" {
		t.Errorf("meter config = enabled:%v org:%q key set:%v", m.Enabled, m.OrgID, m.SessionKey != "")
	}
	if f.applied == 0 {
		t.Error("meter enabled but the daemon was not restarted to start it")
	}
}

func TestMeterSetupToolWritesNothingForARejectedKey(t *testing.T) {
	f := newSetupFixture(t, "")
	f.env[meterKeyEnv] = "sk-ant-sid-expired"
	f.baseURL = meterStub(t, http.StatusUnauthorized)
	got := callTool(t, f.server(), "tokenops_vendor_usage_setup", map[string]any{})
	if got["error"] != "session_key_rejected" {
		t.Fatalf("response = %v", got)
	}
	if f.config().VendorUsage.ClaudeUsageMeter.Enabled || f.applied != 0 {
		t.Error("a rejected key was written or applied")
	}
}

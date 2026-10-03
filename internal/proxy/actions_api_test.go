package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// allowAll stands in for the API authenticator in tests that are past it.
type allowAll struct{}

func (allowAll) Middleware(next http.Handler) http.Handler { return next }

type actionFixture struct {
	path    string
	mu      sync.Mutex
	audited []string
	applied chan struct{}
}

func newActionServer(t *testing.T, auth DashAuth) (*Server, *actionFixture) {
	t.Helper()
	f := &actionFixture{path: filepath.Join(t.TempDir(), "config.yaml"), applied: make(chan struct{}, 4)}
	if err := os.WriteFile(f.path, []byte("log:\n  level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := []Option{WithActions(func() ActionDeps {
		return ActionDeps{
			ConfigPath: f.path,
			Apply: func() (string, func()) {
				return "restarting", func() { f.applied <- struct{}{} }
			},
			Audit: func(_ context.Context, target string, _ map[string]any) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.audited = append(f.audited, target)
			},
		}
	})}
	if auth != nil {
		opts = append(opts, WithDashAuth(auth))
	}
	return New("127.0.0.1:0", opts...), f
}

func post(s *Server, path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	s.apiMux().ServeHTTP(rec, req)
	return rec
}

// A write is never served without the token, however the rest is wired.
func TestActionRoutesNeedTheAPIToken(t *testing.T) {
	s, _ := newActionServer(t, nil)
	if s.ServesAPI(http.MethodPost, "/api/mode") {
		t.Fatal("a write route is mounted without API auth")
	}
}

func TestModeActionWritesAuditsAndAppliesAfterAnswering(t *testing.T) {
	s, f := newActionServer(t, allowAll{})
	rec := post(s, "/api/mode", "application/json", `{"mode":"active"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("mode = %d %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["mode"] != "active" || body["note"] != "restarting" {
		t.Fatalf("body = %v, %v", body, err)
	}
	<-f.applied
	cfg, err := config.ReadMutable(f.path)
	if err != nil || cfg.Mode != "active" {
		t.Fatalf("written mode %q, %v", cfg.Mode, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.audited) != 1 || f.audited[0] != "mode" {
		t.Errorf("audited %v", f.audited)
	}
}

// Only a JSON body is taken, and only the fields an action knows: a form
// post (which a browser can send cross-origin) and a typo are refused.
func TestActionRoutesRefuseWhatTheyDoNotTake(t *testing.T) {
	s, f := newActionServer(t, allowAll{})
	before, _ := os.ReadFile(f.path)
	for _, tc := range []struct{ path, ctype, body string }{
		{"/api/mode", "text/plain", `{"mode":"active"}`},
		{"/api/mode", "application/x-www-form-urlencoded", `mode=active`},
		{"/api/mode", "application/json", `{"mode":"active","extra":1}`},
		{"/api/mode", "application/json", `{"mode":"turbo"}`},
		{"/api/budgets", "application/json", `{"name":"x","delete":true}`},
		{"/api/routing/rules", "application/json", `{"provider":"anthropic"}`},
		{"/api/plans", "application/json", `{"plan":"claude-max-20x"}`},
	} {
		if rec := post(s, tc.path, tc.ctype, tc.body); rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s %s %s = %d, want 400", tc.path, tc.ctype, tc.body, rec.Code)
		}
	}
	if after, _ := os.ReadFile(f.path); string(after) != string(before) {
		t.Error("a refused write touched the config")
	}
	if len(f.audited) != 0 {
		t.Errorf("refused writes were audited: %v", f.audited)
	}
}

func TestBudgetRuleAndPlanActions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, f := newActionServer(t, allowAll{})
	for _, tc := range []struct{ path, body string }{
		{"/api/budgets", `{"name":"monthly","limit_usd":50}`},
		{"/api/routing/rules", `{"provider":"anthropic","from_model":"claude-opus-5","to_model":"claude-sonnet-5","quality":0.9}`},
		{"/api/plans", `{"provider":"anthropic","plan":"claude-max-20x"}`},
	} {
		if rec := post(s, tc.path, "application/json", tc.body); rec.Code != http.StatusOK {
			t.Fatalf("POST %s = %d %s", tc.path, rec.Code, rec.Body)
		}
		<-f.applied
	}
	cfg, err := config.ReadMutable(f.path)
	if err != nil || len(cfg.Budgets) != 1 || len(cfg.Optimizer.RoutingRules) != 1 || cfg.Plans["anthropic"] != "claude-max-20x" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
}

func TestPreferredModelRoutingDecisionAndOutcomeActions(t *testing.T) {
	s, f := newActionServer(t, allowAll{})

	if rec := post(s, "/api/preferred-models", "application/json", `{"provider":"anthropic","model":"claude-sonnet-5"}`); rec.Code != http.StatusOK {
		t.Fatalf("preferred model = %d %s", rec.Code, rec.Body)
	}
	<-f.applied // a config change restarts the daemon
	if cfg, _ := config.ReadMutable(f.path); cfg.PreferredModels["anthropic"] != "claude-sonnet-5" {
		t.Errorf("preferred model not written: %v", cfg.PreferredModels)
	}

	// The proposals live under HOME, which TestMain sandboxes.
	rec := post(s, "/api/routing/decisions", "application/json", `{"key":"anthropic|a|b","decision":"approve"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no routing proposal") {
		t.Errorf("unknown proposal = %d %s", rec.Code, rec.Body)
	}

	// No store wired: the outcome is refused as storage_disabled, not lost.
	rec = post(s, "/api/outcomes", "application/json", `{"execution_id":"e1","result":"achieved"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "storage_disabled") {
		t.Errorf("outcome = %d %s", rec.Code, rec.Body)
	}
	if rec := post(s, "/api/outcomes", "application/json", `{"execution_id":"e1","result":"done"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad result = %d", rec.Code)
	}
	select {
	case <-f.applied:
		t.Error("a routing decision or outcome restarted the daemon")
	default:
	}
}

func TestCoachActionChangesDials(t *testing.T) {
	s, f := newActionServer(t, allowAll{})
	rec := post(s, "/api/coach", "application/json", `{"verbosity":"quiet"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"verbosity":"quiet"`) {
		t.Fatalf("coach = %d %s", rec.Code, rec.Body)
	}
	if cfg, _ := config.ReadMutable(f.path); cfg.Coach.Verbosity != "quiet" {
		t.Errorf("verbosity not written: %q", cfg.Coach.Verbosity)
	}
	for _, body := range []string{`{}`, `{"power":"waste"}`, `{"verbosity":"shouty"}`} {
		if rec := post(s, "/api/coach", "application/json", body); rec.Code != http.StatusBadRequest {
			t.Errorf("POST /api/coach %s = %d, want 400", body, rec.Code)
		}
	}
}

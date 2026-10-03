package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
)

func stateServer(cfg *config.Config) *Server {
	return New("127.0.0.1:0", WithState(func() state.Deps {
		return state.Deps{
			Config: cfg,
			Ready:  func() bool { return true },
			Count: func(context.Context, time.Time, time.Time) (map[string]int64, error) {
				return map[string]int64{"codex-jsonl": 7}, nil
			},
			Coach: func(time.Time) coachcap.Report { return coachcap.Report{Verbosity: "quiet"} },
		}
	}))
}

func TestStateRoutesAnswer(t *testing.T) {
	cfg := config.Default()
	s := stateServer(&cfg)

	var st state.Status
	if code := getAPI(t, s, "/api/status", &st); code != http.StatusOK || st.State == "" || st.Insight.Level == "" {
		t.Errorf("status = %d %+v", code, st)
	}
	var mode state.Mode
	if code := getAPI(t, s, "/api/mode", &mode); code != http.StatusOK || mode.Mode != config.ModePassive || len(mode.Subsystems) == 0 {
		t.Errorf("mode = %d %+v", code, mode)
	}
	var coach coachcap.Report
	if code := getAPI(t, s, "/api/coach", &coach); code != http.StatusOK || coach.Verbosity != "quiet" {
		t.Errorf("coach = %d %+v", code, coach)
	}
	var ds state.DataSources
	if code := getAPI(t, s, "/api/data-sources?since=24h", &ds); code != http.StatusOK || ds.Counts["codex-jsonl"] != 7 {
		t.Errorf("data sources = %d %+v", code, ds)
	}
	var vu state.VendorUsage
	if code := getAPI(t, s, "/api/vendor-usage?window_hours=6", &vu); code != http.StatusOK || vu.WindowHours != 6 || len(vu.Sources) == 0 {
		t.Errorf("vendor usage = %d %+v", code, vu)
	}
}

// The config route serves the redacted snapshot; a secret in config must
// never reach it.
func TestConfigRouteRedactsSecrets(t *testing.T) {
	cfg := config.Default()
	cfg.VendorUsage.ClaudeUsageMeter.SessionKey = "sk-ant-sid-secret-value"
	cfg.VendorUsage.Anthropic.AdminKey = "sk-ant-admin-secret-value"
	rec := httptest.NewRecorder()
	stateServer(&cfg).apiMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("config = %d", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "secret-value") {
		t.Fatalf("config leaked a secret: %s", body)
	}
}

func TestStateRoutesRejectBadQueries(t *testing.T) {
	cfg := config.Default()
	s := stateServer(&cfg)
	for _, path := range []string{"/api/data-sources?since=yesterday", "/api/vendor-usage?window_hours=-1"} {
		rec := httptest.NewRecorder()
		s.apiMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
	}
}

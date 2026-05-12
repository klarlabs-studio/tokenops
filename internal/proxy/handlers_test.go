package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestReadyzExposesBlockersAndNextActions verifies that /readyz surfaces
// the structured blockers + next_actions the daemon publishes so a
// fresh-install operator can see what to fix without grepping config.
func TestReadyzExposesBlockersAndNextActions(t *testing.T) {
	// Snapshot + restore global state so the test is order-independent.
	prevBlockers, prevNext := snapshotReadyState()
	ready.Store(false)
	SetReadyState(
		[]string{"storage_disabled", "providers_unconfigured"},
		[]string{"run `tokenops init` then restart the daemon"},
	)
	t.Cleanup(func() { SetReadyState(prevBlockers, prevNext) })

	rec := httptest.NewRecorder()
	readyzHandler(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	var body struct {
		Status      string   `json:"status"`
		Blockers    []string `json:"blockers"`
		NextActions []string `json:"next_actions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "not_configured" {
		t.Errorf("status=%q want not_configured", body.Status)
	}
	if len(body.Blockers) != 2 || body.Blockers[0] != "storage_disabled" {
		t.Errorf("blockers=%v", body.Blockers)
	}
	if len(body.NextActions) != 1 {
		t.Errorf("next_actions=%v", body.NextActions)
	}
}

// TestDisabledSubsystemReturnsStructuredError verifies that operators
// hitting analytics or rules routes without those subsystems enabled
// receive 503 + {error, hint} instead of an opaque 404 — the core
// activation-UX fix for first-run installs.
func TestDisabledSubsystemReturnsStructuredError(t *testing.T) {
	srv := New("127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})

	cases := []struct {
		path      string
		wantError string
	}{
		{"/api/spend/summary", "storage_disabled"},
		{"/api/spend/forecast", "storage_disabled"},
		{"/api/optimizations", "storage_disabled"},
		{"/api/audit", "storage_disabled"},
		{"/api/rules/analyze", "rules_disabled"},
		{"/api/rules/inject", "rules_disabled"},
	}
	client := &http.Client{Timeout: 2 * time.Second}
	base := "http://" + srv.Addr()
	for _, tc := range cases {
		resp, err := client.Get(base + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s: status=%d want 503; body=%s", tc.path, resp.StatusCode, body)
			continue
		}
		var parsed struct {
			Error string `json:"error"`
			Hint  string `json:"hint"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Errorf("%s: decode body: %v (%s)", tc.path, err, body)
			continue
		}
		if parsed.Error != tc.wantError {
			t.Errorf("%s: error=%q want %q", tc.path, parsed.Error, tc.wantError)
		}
		if parsed.Hint == "" {
			t.Errorf("%s: missing hint", tc.path)
		}
	}
}

// TestReadyzReadyOmitsBlockers confirms that once the daemon marks
// itself ready the endpoint returns 200 with empty blocker arrays
// rather than null values.
func TestReadyzReadyOmitsBlockers(t *testing.T) {
	prevBlockers, prevNext := snapshotReadyState()
	ready.Store(true)
	SetReadyState(nil, nil)
	t.Cleanup(func() {
		SetReadyState(prevBlockers, prevNext)
		ready.Store(false)
	})

	rec := httptest.NewRecorder()
	readyzHandler(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Status      string   `json:"status"`
		Blockers    []string `json:"blockers"`
		NextActions []string `json:"next_actions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ready" {
		t.Errorf("status=%q want ready", body.Status)
	}
	if body.Blockers == nil {
		t.Errorf("blockers should be empty slice, got nil — JSON would be null")
	}
}

package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The reader follows the pages, sums every user's record and keeps no
// identity.
func TestClaudeCodeDaySumsUsersAcrossPages(t *testing.T) {
	record := func(actor map[string]string, sessions int, model string, cents float64) map[string]any {
		return map[string]any{
			"date": "2026-10-30T00:00:00Z", "actor": actor, "customer_type": "api", "terminal_type": "vscode",
			"core_metrics": map[string]any{"num_sessions": sessions, "lines_of_code": map[string]any{"added": 100, "removed": 40},
				"commits_by_claude_code": 2, "pull_requests_by_claude_code": 1},
			"tool_actions": map[string]any{"edit_tool": map[string]any{"accepted": 9, "rejected": 1},
				"write_tool": map[string]any{"accepted": 1, "rejected": 1}},
			"model_breakdown": []any{map[string]any{"model": model,
				"tokens":         map[string]any{"input": 1000, "output": 200, "cache_read": 5000, "cache_creation": 300},
				"estimated_cost": map[string]any{"currency": "USD", "amount": cents}}},
		}
	}
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organizations/usage_report/claude_code" || r.Header.Get("x-api-key") != "sk-ant-admin-x" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if got := r.URL.Query().Get("starting_at"); got != "2026-10-30" {
			t.Errorf("starting_at = %q", got)
		}
		pages = append(pages, r.URL.Query().Get("page"))
		var body map[string]any
		if r.URL.Query().Get("page") == "" {
			body = map[string]any{"data": []any{
				record(map[string]string{"type": "user_actor", "email_address": "ann@example.com"}, 3, "claude-opus-5-5", 150),
				record(map[string]string{"type": "user_actor", "email_address": "ben@example.com"}, 2, "claude-sonnet-5-5", 50),
			}, "has_more": true, "next_page": "page_2"}
		} else {
			body = map[string]any{"data": []any{
				record(map[string]string{"type": "api_actor", "api_key_name": "ci"}, 1, "claude-opus-5-5", 100),
			}, "has_more": false, "next_page": nil}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	c := &AdminClient{BaseURL: srv.URL, AdminKey: "sk-ant-admin-x"}
	got, err := c.ClaudeCodeDay(context.Background(), time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[1] != "page_2" {
		t.Errorf("pages = %q", pages)
	}
	if got.Day != "2026-10-30" || got.Users != 2 || got.APIKeys != 1 || got.Sessions != 6 || got.Commits != 6 ||
		got.PullRequests != 3 || got.LinesAdded != 300 || got.LinesRemoved != 120 {
		t.Errorf("day = %+v", got)
	}
	if d := got.Decisions(); d.Accepted != 30 || d.Rejected != 6 {
		t.Errorf("decisions = %+v", d)
	}
	if usd := got.EstimatedUSD(); usd != 3 {
		t.Errorf("estimated = %v, want 3 (cents converted)", usd)
	}
	if m := got.Models["claude-opus-5-5"]; m.InputTokens != 2000 || m.EstimatedUSD != 2.5 {
		t.Errorf("opus = %+v", m)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "example.com") || strings.Contains(string(raw), `"ci"`) {
		t.Errorf("an identity survived: %s", raw)
	}
}

func TestClaudeCodeDayRefusesOtherCurrenciesAndMissingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"actor":{"type":"user_actor"},"model_breakdown":[{"model":"m","estimated_cost":{"currency":"EUR","amount":1}}]}],"has_more":false}`))
	}))
	defer srv.Close()
	if _, err := (&AdminClient{BaseURL: srv.URL, AdminKey: "k"}).ClaudeCodeDay(context.Background(), time.Now()); err == nil {
		t.Error("EUR accepted")
	}
	if _, err := (&AdminClient{BaseURL: srv.URL}).ClaudeCodeDay(context.Background(), time.Now()); err == nil {
		t.Error("no key accepted")
	}
}

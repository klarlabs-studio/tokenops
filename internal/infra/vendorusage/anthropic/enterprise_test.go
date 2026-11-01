package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
)

func TestEnterpriseClaudeCodeDaySumsActiveUsers(t *testing.T) {
	user := func(email string, sessions any, commits int, accepted int) map[string]any {
		return map[string]any{
			"user": map[string]any{"id": "user_" + email, "email_address": email, "type": "user_actor"},
			"claude_code_metrics": map[string]any{
				"core_metrics": map[string]any{"commit_count": commits, "distinct_session_count": sessions, "pull_request_count": 1,
					"lines_of_code": map[string]any{"added_count": 50, "removed_count": 10}},
				"tool_actions": map[string]any{"edit_tool": map[string]any{"accepted_count": accepted, "rejected_count": 1},
					"write_tool": map[string]any{"accepted_count": 0, "rejected_count": 0}},
			},
		}
	}
	idle := map[string]any{"user": map[string]any{"email_address": "idle@example.com"},
		"claude_code_metrics": map[string]any{"core_metrics": map[string]any{"distinct_session_count": nil}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organizations/analytics/users" || r.Header.Get("x-api-key") != "ak" ||
			r.Header.Get("Authorization") != "Bearer ak" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Query().Get("date") {
		case "2026-10-31":
		default:
			http.Error(w, `{"error":{"message":"data for 2026-11-01 is not yet available; the most recent available day is 2026-10-31"}}`, http.StatusBadRequest)
			return
		}
		var body map[string]any
		if r.URL.Query().Get("page") == "" {
			body = map[string]any{"data": []any{user("ann@example.com", 4, 2, 9), idle}, "next_page": "p2"}
		} else {
			body = map[string]any{"data": []any{user("ben@example.com", nil, 1, 3)}, "next_page": nil}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	c := &AnalyticsClient{BaseURL: srv.URL, Key: "ak"}
	got, err := c.ClaudeCodeDay(context.Background(), time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got.Users != 2 || got.Sessions != 4 || got.Commits != 3 || got.PullRequests != 2 || got.LinesAdded != 100 {
		t.Errorf("day = %+v", got)
	}
	if d := got.Decisions(); d.Accepted != 12 || d.Rejected != 2 {
		t.Errorf("decisions = %+v", d)
	}
	if len(got.Models) != 0 {
		t.Errorf("models from a report without cost: %+v", got.Models)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "example.com") {
		t.Errorf("an identity survived: %s", raw)
	}
	if _, err := c.ClaudeCodeDay(context.Background(), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, usage.ErrDayNotAvailable) {
		t.Errorf("unpublished day: %v", err)
	}
	if _, err := (&AnalyticsClient{BaseURL: srv.URL}).ClaudeCodeDay(context.Background(), time.Now()); !errors.Is(err, ErrMissingAnalyticsKey) {
		t.Errorf("no key: %v", err)
	}
}

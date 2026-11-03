package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSpendLimitsFollowsPagesAndSkipsKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organizations/spend_limits/effective" || r.Header.Get("x-api-key") != "ek" ||
			r.URL.Query().Get("limit") != "1000" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		ann := map[string]any{
			"actor":  map[string]any{"type": "user_actor", "user_id": "user_ann", "name": "Ann", "email_address": "ann@example.com"},
			"amount": "50000", "currency": "USD", "period": "monthly", "period_to_date_spend": "41280.125",
			"source": map[string]any{"type": "seat_tier", "seat_tier": "enterprise_standard"}, "spend_limit_id": "spl_1",
		}
		key := map[string]any{"actor": map[string]any{"type": "scoped_api_key_actor", "scoped_api_key_id": "k"},
			"amount": nil, "currency": "USD", "period": "monthly", "period_to_date_spend": "0"}
		ben := map[string]any{
			"actor":  map[string]any{"type": "user_actor", "user_id": "user_ben", "name": nil, "email_address": nil, "deleted": true},
			"amount": nil, "currency": "USD", "period": "monthly", "period_to_date_spend": "0",
			"source": map[string]any{"type": "organization"},
		}
		body := map[string]any{"data": []any{ann, key}, "next_page": "p2"}
		if r.URL.Query().Get("page") == "p2" {
			body = map[string]any{"data": []any{ben}, "next_page": nil}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	got, err := (&AnalyticsClient{BaseURL: srv.URL, Key: "ek"}).SpendLimits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %+v", got)
	}
	if a := got[0]; a.Email != "ann@example.com" || a.Limit == nil || *a.Limit != 50000 || a.Spent != 41280.125 ||
		a.Source != "seat_tier" || a.SourceID != "enterprise_standard" {
		t.Errorf("ann = %+v", a)
	}
	if b := got[1]; b.Limit != nil || b.Name != "" || b.Source != "organization" {
		t.Errorf("ben = %+v", b)
	}
}

func TestPendingIncreaseRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organizations/spend_limit_increase_requests" || r.URL.Query().Get("status[]") != "pending" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{
			"id": "slir_1", "status": "pending", "period": "monthly", "created_at": "2026-11-02T09:30:00Z",
			"actor": map[string]any{"type": "user_actor", "user_id": "user_ann", "email_address": "ann@example.com"},
		}}, "next_page": nil})
	}))
	defer srv.Close()
	got, err := (&AnalyticsClient{BaseURL: srv.URL, Key: "ek"}).PendingIncreaseRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := IncreaseRequest{ID: "slir_1", UserID: "user_ann", Email: "ann@example.com", Period: "monthly",
		CreatedAt: time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("requests = %+v", got)
	}
}

func TestSpendLimitsScopeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"key has read:analytics; endpoint needs read:spend_limits"}}`, http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := (&AnalyticsClient{BaseURL: srv.URL, Key: "ek"}).SpendLimits(context.Background())
	if !errors.Is(err, ErrSpendLimitsScope) {
		t.Fatalf("err = %v, want the scope error", err)
	}
	if _, err := (&AnalyticsClient{}).SpendLimits(context.Background()); !errors.Is(err, ErrMissingAnalyticsKey) {
		t.Fatalf("no key: err = %v", err)
	}
}

package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrgBudgetsFollowsPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/organizations/acme/settings/billing/budgets" || r.Header.Get("Authorization") != "Bearer gt" ||
			r.Header.Get("X-GitHub-Api-Version") != budgetsAPIVersion || r.URL.Query().Get("per_page") != "100" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// The shape GitHub sent for a live organisation: no consumed_amount
		// on a budget with nothing spent.
		body := map[string]any{"budgets": []any{map[string]any{
			"id": "b1", "budget_type": "ProductPricing", "budget_product_sku": "actions", "budget_scope": "organization",
			"budget_amount": 0, "prevent_further_usage": true, "budget_entity_name": "acme",
			"budget_alerting": map[string]any{"will_alert": true, "alert_recipients": []string{"owner"}},
		}}, "has_next_page": true, "total_count": 2}
		if r.URL.Query().Get("page") == "2" {
			body = map[string]any{"budgets": []any{map[string]any{
				"id": "b2", "budget_type": "SkuPricing", "budget_product_sku": "premium_requests", "budget_scope": "user",
				"budget_amount": 30, "prevent_further_usage": true, "budget_entity_name": "", "user": "annb", "consumed_amount": 12.5,
			}}, "has_next_page": false, "total_count": 2}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	got, err := (&BudgetClient{BaseURL: srv.URL, Token: "gt"}).OrgBudgets(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Consumed != nil || got[0].Entity != "acme" || !got[0].Stops {
		t.Fatalf("budgets = %+v", got)
	}
	if b := got[1]; b.User != "annb" || b.Consumed == nil || *b.Consumed != 12.5 || b.Amount != 30 || !IsCopilotSKU(b.SKU) {
		t.Errorf("user budget = %+v", b)
	}
}

// A budget GitHub gave no amount for is left out: read as $0 with usage
// stopped, it would report its holder as blocked.
func TestABudgetWithoutAnAmountIsNoCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"budgets":[
			{"id":"b1","budget_product_sku":"copilot","budget_scope":"user","user":"annb","prevent_further_usage":true},
			{"id":"b2","budget_product_sku":"copilot","budget_scope":"user","user":"zed","budget_amount":0,"prevent_further_usage":true}
		],"has_next_page":false}`))
	}))
	defer srv.Close()
	got, err := (&BudgetClient{BaseURL: srv.URL, Token: "gt"}).OrgBudgets(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "b2" || got[0].Amount != 0 || !got[0].Stops {
		t.Errorf("budgets = %+v, want only the one GitHub gave an amount for", got)
	}
}

func TestOrgBudgetsAccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Must have admin rights"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := (&BudgetClient{BaseURL: srv.URL, Token: "gt"}).OrgBudgets(context.Background(), "acme"); !errors.Is(err, ErrBudgetsAccess) {
		t.Fatalf("err = %v", err)
	}
	if _, err := (&BudgetClient{}).OrgBudgets(context.Background(), "acme"); !errors.Is(err, ErrNoGitHubToken) {
		t.Fatalf("no token: err = %v", err)
	}
}

func TestIsCopilotSKU(t *testing.T) {
	for sku, want := range map[string]bool{"copilot": true, "copilot_premium_request": true, "premium_requests": true,
		"ai_credits": true, "actions": false, "codespaces": false} {
		if IsCopilotSKU(sku) != want {
			t.Errorf("IsCopilotSKU(%q) = %v", sku, !want)
		}
	}
}

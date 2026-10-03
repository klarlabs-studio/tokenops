package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// serveRaw is serve for z.ai, which takes the key without "Bearer".
func serveRaw(t *testing.T, path, key, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != key || r.URL.Path != path {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func approx(a, b float64) bool { return a-b < 0.001 && b-a < 0.001 }

// Shapes below are the vendors' documented examples or their own clients'
// fixtures (see each reader's comment), not live answers.

func TestZAIReadsTheTokenWindows(t *testing.T) {
	body := `{"success":true,"code":200,"data":{"limits":[
	 {"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":37,"nextResetTime":1791000000000},
	 {"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":"12"},
	 {"type":"TIME_LIMIT","unit":5,"number":1,"percentage":90}]}}`
	srv := serveRaw(t, "/api/monitor/usage/quota/limit", "zk", body)
	defer srv.Close()
	got, err := ZAI{BaseURL: srv.URL}.Read(context.Background(), "zk")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || w.UsedPct != 37 || !w.ResetsAt.Equal(time.UnixMilli(1791000000000)) {
		t.Errorf("5h window %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || w.UsedPct != 12 {
		t.Errorf("week window %+v", w)
	}
	srv2 := serveRaw(t, "/api/monitor/usage/quota/limit", "zk", `{"success":false,"msg":"bad plan","code":500}`)
	defer srv2.Close()
	if _, err := (ZAI{BaseURL: srv2.URL}).Read(context.Background(), "zk"); err == nil || errors.Is(err, ErrAuth) {
		t.Errorf("an unsuccessful answer = %v", err)
	}
	// As seen live: a 200 whose body refuses the key.
	refused := serveRaw(t, "/api/monitor/usage/quota/limit", "zk", `{"code":1001,"msg":"Authentication parameter not received in Header","success":false}`)
	defer refused.Close()
	if _, err := (ZAI{BaseURL: refused.URL}).Read(context.Background(), "zk"); !errors.Is(err, ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
}

func TestKimiReadsRatios(t *testing.T) {
	body := `{"usages":{"limit_5h":{"used_ratio":0.3,"reset_time":"2026-09-11T18:00:00Z"},
	 "limit_7d":{"used_ratio":"0.2","reset_time":"2026-09-14T00:00:00Z"},
	 "limit_month_total":{"used_ratio":0.4,"reset_time":"2026-10-01T00:00:00Z"}}}`
	srv := serve(t, "/coding/v1/usages", "kk", body)
	defer srv.Close()
	got, err := Kimi{BaseURL: srv.URL}.Read(context.Background(), "kk")
	if err != nil || len(got.Windows) != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 30) || w.ResetsAt.IsZero() {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 20) {
		t.Errorf("week %+v", w)
	}
}

func TestMiniMaxTurnsRemainingIntoUsed(t *testing.T) {
	body := `{"model_remains":[
	 {"model_name":"video","current_interval_remaining_percent":5,"current_interval_status":1},
	 {"model_name":"general","start_time":1782043200000,"end_time":1782061200000,
	  "current_interval_remaining_percent":100,"current_interval_status":1,
	  "weekly_end_time":1782604800000,"current_weekly_remaining_percent":70,"current_weekly_status":1}],
	 "base_resp":{"status_code":0,"status_msg":"success"}}`
	srv := serve(t, "/v1/token_plan/remains", "mk", body)
	defer srv.Close()
	got, err := MiniMax{BaseURL: srv.URL}.Read(context.Background(), "mk")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || w.UsedPct != 0 {
		t.Errorf("interval %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || w.UsedPct != 30 {
		t.Errorf("weekly %+v", w)
	}
	bad := serve(t, "/v1/token_plan/remains", "mk", `{"base_resp":{"status_code":1004,"status_msg":"login fail"}}`)
	defer bad.Close()
	if _, err := (MiniMax{BaseURL: bad.URL}).Read(context.Background(), "mk"); !errors.Is(err, ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
}

func TestSyntheticRequestQuota(t *testing.T) {
	srv := serve(t, "/v2/quotas", "sk", `{"subscription":{"limit":135,"requests":27,"renewsAt":"2025-09-21T14:36:14.288Z"}}`)
	defer srv.Close()
	got, err := Synthetic{BaseURL: srv.URL}.Read(context.Background(), "sk")
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 20) || got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestChutesCapsAndNoSubscription(t *testing.T) {
	body := `{"subscription":true,"four_hour":{"usage":1.5,"cap":6,"remaining":4.5,"reset_at":"2026-10-03T16:00:00.123456"},
	 "monthly":{"uncapped":true}}`
	srv := serve(t, "/users/me/subscription_usage", "ck", body)
	defer srv.Close()
	got, err := Chutes{BaseURL: srv.URL}.Read(context.Background(), "ck")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != "4h" || !approx(got.Windows[0].UsedPct, 25) || got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
	none := serve(t, "/users/me/subscription_usage", "ck", `{"subscription":false}`)
	defer none.Close()
	if got, err := (Chutes{BaseURL: none.URL}).Read(context.Background(), "ck"); err != nil || !got.Empty() {
		t.Errorf("no subscription = %+v, %v", got, err)
	}
}

func TestDeepInfraChecklist(t *testing.T) {
	srv := serve(t, "/payment/checklist", "dk", `{"stripe_balance":-12.5,"recent":3.25,"limit":null,"suspended":false}`)
	defer srv.Close()
	got, err := DeepInfra{BaseURL: srv.URL}.Read(context.Background(), "dk")
	if err != nil || got.UsedUSD != 3.25 || !got.HasUsed || got.BalanceUSD != 12.5 || !got.HasBalance || got.LimitUSD != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestVercelCreditsAreStrings(t *testing.T) {
	srv := serve(t, "/v1/credits", "vk", `{"balance":"95.50","total_used":"4.50"}`)
	defer srv.Close()
	got, err := Vercel{BaseURL: srv.URL}.Read(context.Background(), "vk")
	if err != nil || got.BalanceUSD != 95.5 || !got.HasBalance || got.Scope != "team" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// A subscription is stored with its windows and without the pay-as-you-go
// attributes, so headroom does not bind it as per-token billing.
func TestSubscriptionEnvelope(t *testing.T) {
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	env := NewEnvelope(at, Kimi{}, Reading{Scope: "account", Subscription: true, Windows: []Window{
		{Name: "5h", UsedPct: 30, Duration: 5 * time.Hour, ResetsAt: at.Add(time.Hour)},
	}})
	a := env.Attributes
	if a["billing"] != "subscription" || a["window_0_name"] != "5h" || a["window_0_used_pct"] != "30.00" ||
		a["window_0_duration_min"] != "300" || a["window_0_reset_at"] != "2026-10-03T13:00:00Z" {
		t.Errorf("attributes %v", a)
	}
	if _, ok := a["extra_usage_limit"]; ok {
		t.Error("a subscription carries pay-as-you-go attributes")
	}
}

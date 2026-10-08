package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// The fixture is a quota-limited key's answer as sub2api's
// GatewayHandler.usageQuotaLimited writes it and CodexBar's sub2api.js
// parses it.
func TestSub2APIQuotaLimitedKey(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/setup/status", `{"code":0,"data":{"needs_setup":false,"step":"completed"}}`,
		"/v1/usage", "Authorization", "Bearer ", fixture(t, "sub2api"))
	ctx := context.Background()
	if !(Sub2API{}).Recognise(ctx, srv.URL) || (LiteLLM{}).Recognise(ctx, srv.URL) {
		t.Fatal("recognition")
	}
	got, err := Sub2API{}.Read(ctx, srv.URL, "vk")
	if err != nil || got.Subscription || !got.HasUsed || got.UsedUSD != 12.5 || got.LimitUSD != 50 || got.LimitReached || got.HasBalance {
		t.Fatalf("got %+v, %v", got, err)
	}
	want := []struct {
		name  string
		pct   float64
		d     time.Duration
		reset bool
	}{{"5h", 40, 5 * time.Hour, true}, {"day", 25, 24 * time.Hour, true}, {"week", 31.25, 7 * 24 * time.Hour, false}}
	if len(got.Windows) != len(want) {
		t.Fatalf("windows %+v", got.Windows)
	}
	for i, w := range want {
		g := got.Windows[i]
		if g.Name != w.name || !approx(g.UsedPct, w.pct) || g.Duration != w.d || g.ResetsAt.IsZero() == w.reset {
			t.Errorf("window %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestSub2APISubscriptionAndWallet(t *testing.T) {
	ctx := context.Background()
	var sub fakeGateway
	srv := sub.server(t, "/health", `{"status":"ok"}`, "/v1/usage", "Authorization", "Bearer ",
		`{"mode":"unrestricted","isValid":true,"planName":"Claude","unit":"USD","remaining":3,
		  "subscription":{"daily_usage_usd":2,"weekly_usage_usd":9,"monthly_usage_usd":30,"daily_limit_usd":10,"weekly_limit_usd":null,"monthly_limit_usd":120,"expires_at":"2026-11-01T00:00:00Z"}}`)
	got, err := Sub2API{}.Read(ctx, srv.URL, "vk")
	if err != nil || !got.Subscription || got.HasUsed || len(got.Windows) != 2 {
		t.Fatalf("subscription %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "day" || !approx(w.UsedPct, 20) || !w.ResetsAt.IsZero() {
		t.Errorf("daily %+v", w)
	}
	if w := got.Windows[1]; w.Name != "month" || !approx(w.UsedPct, 25) {
		t.Errorf("monthly %+v", w)
	}

	var wallet fakeGateway
	srv = wallet.server(t, "/health", `{}`, "/v1/usage", "Authorization", "Bearer ",
		`{"mode":"unrestricted","isValid":true,"planName":"wallet","remaining":7.25,"unit":"USD","balance":7.25}`)
	got, err = Sub2API{}.Read(ctx, srv.URL, "vk")
	if err != nil || !got.HasBalance || got.BalanceUSD != 7.25 || len(got.Windows) != 0 || got.Subscription {
		t.Fatalf("wallet %+v, %v", got, err)
	}
}

func TestSub2APIRefusals(t *testing.T) {
	ctx := context.Background()
	var f fakeGateway
	srv := f.server(t, "/setup/status", `{}`, "/v1/usage", "Authorization", "Bearer ", `{"isValid":false,"status":"disabled"}`)
	if (Sub2API{}).Recognise(ctx, srv.URL) {
		t.Error("recognised a server without sub2api's setup status")
	}
	if _, err := (Sub2API{}).Read(ctx, srv.URL, "wrong"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("401: %v", err)
	}
	// sub2api answers 200 with isValid false for a key it disabled.
	if _, err := (Sub2API{}).Read(ctx, srv.URL, "vk"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("isValid false: %v", err)
	}
	var exhausted fakeGateway
	srv = exhausted.server(t, "/x", "", "/v1/usage", "Authorization", "Bearer ",
		`{"mode":"quota_limited","isValid":true,"status":"quota_exhausted","quota":{"limit":5,"used":5,"remaining":0,"unit":"USD"}}`)
	if got, err := (Sub2API{}).Read(ctx, srv.URL, "vk"); err != nil || !got.LimitReached {
		t.Errorf("exhausted %+v, %v", got, err)
	}
	var unknown fakeGateway
	srv = unknown.server(t, "/x", "", "/v1/usage", "Authorization", "Bearer ", `{"mode":"something new"}`)
	if got, err := (Sub2API{}).Read(ctx, srv.URL, "vk"); err != nil || !got.Empty() {
		t.Errorf("an answer with nothing known is an empty reading: %+v, %v", got, err)
	}
}

package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var octoberEighthNoon = func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }

// The fixture is /v1/quota-stats as CodexBar's llmproxy.ts parses it:
// quota_groups keyed or listed, each with the share left and its reset.
func TestLLMProxyTakesTheTightestGroup(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/", `{"Status": "API Key Proxy is running"}`, "/v1/quota-stats", "Authorization", "Bearer ", fixture(t, "llm-proxy"))
	ctx := context.Background()
	if !(LLMProxy{}).Recognise(ctx, srv.URL) || (Sub2API{}).Recognise(ctx, srv.URL) {
		t.Fatal("recognition")
	}
	got, err := LLMProxy{Now: octoberEighthNoon}.Read(ctx, srv.URL, "vk")
	if err != nil || len(got.Windows) != 1 || got.HasUsed || got.Subscription || got.LimitReached {
		t.Fatalf("got %+v, %v", got, err)
	}
	// gemini_cli/pro has 35% left; the soonest reset still ahead is flash's
	// (openai's is in the past).
	w := got.Windows[0]
	if w.Name != "quota" || !approx(w.UsedPct, 65) || w.ResetsAt.Format(time.RFC3339) != "2026-10-08T16:00:00Z" {
		t.Errorf("window %+v", w)
	}
}

func TestLLMProxyWindowShape(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/", `{}`, "/v1/quota-stats", "Authorization", "Bearer ",
		`{"providers":{"gemini_cli":{"quota_groups":{"pro":{"windows":{"5h":{"total_used":30,"total_max":40,"remaining_pct":25.0},"daily":{"remaining_pct":null}}}}}}}`)
	got, err := LLMProxy{Now: octoberEighthNoon}.Read(context.Background(), srv.URL, "vk")
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 75) || !got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestLLMProxyRefusedAndEmpty(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/", `{"Status": "something else"}`, "/v1/quota-stats", "Authorization", "Bearer ",
		`{"providers":{"x":{"quota_groups":"malformed"}},"summary":{"approx_cost":3}}`)
	ctx := context.Background()
	if (LLMProxy{}).Recognise(ctx, srv.URL) {
		t.Error("recognised another server")
	}
	if _, err := (LLMProxy{}).Read(ctx, srv.URL, "wrong"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused key: %v", err)
	}
	// No quota groups: nothing to report, and approx_cost, a running total
	// with no period, is not spend.
	if got, err := (LLMProxy{}).Read(ctx, srv.URL, "vk"); err != nil || !got.Empty() {
		t.Errorf("got %+v, %v", got, err)
	}
}

package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// The fixture follows the key.usage contract CodexBar's aixy.ts checks:
// budgets with their enforcement and availability, and 7-day usage.
func TestAixyReadsApplicableBudgets(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/none", "", "/v1/usage", "Authorization", "Bearer ", fixture(t, "aixy"))
	ctx := context.Background()
	got, err := Aixy{}.Read(ctx, srv.URL, "vk")
	if err != nil {
		t.Fatal(err)
	}
	// The hard project budget binds: spent plus reserved against its limit.
	// The unknown key budget is left out, not shown as zero; the 7-day
	// estimate is not read as spend.
	if !got.HasUsed || got.UsedUSD != 50 || got.LimitUSD != 200 || got.LimitReached || got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v", got)
	}
	month := 31 * 24 * time.Hour
	if w := got.Windows[0]; w.Name != "project monthly" || !approx(w.UsedPct, 25) || w.Duration != month || w.ResetsAt.Format(time.RFC3339) != "2026-11-01T00:00:00Z" {
		t.Errorf("hard budget %+v", w)
	}
	if w := got.Windows[1]; w.Name != "organization monthly (shared) (monitor)" || !approx(w.UsedPct, 90) {
		t.Errorf("monitor budget %+v", w)
	}
}

func TestAixyRecognisesTheHostedGatewayOnly(t *testing.T) {
	ctx := context.Background()
	if !(Aixy{}).Recognise(ctx, "https://api.aixy-gateway.com") || (Aixy{}).Recognise(ctx, "https://gw.example") {
		t.Error("recognition")
	}
}

func TestAixyRefusalsAndContract(t *testing.T) {
	ctx := context.Background()
	var f fakeGateway
	srv := f.server(t, "/none", "", "/v1/usage", "Authorization", "Bearer ", `{"object":"key.usage","currency":"EUR","budgets":[]}`)
	if _, err := (Aixy{}).Read(ctx, srv.URL, "revoked"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused key: %v", err)
	}
	if _, err := (Aixy{}).Read(ctx, srv.URL, "vk"); !errors.Is(err, errAixyContract) {
		t.Errorf("another currency: %v", err)
	}
	var none fakeGateway
	srv = none.server(t, "/none", "", "/v1/usage", "Authorization", "Bearer ",
		`{"object":"key.usage","currency":"USD","budgets":[],"usage":{"window":"7d","requests":3,"total_tokens":10,"attributed_requests":3,"partial_requests":0,"spend_usd":0.4}}`)
	if got, err := (Aixy{}).Read(ctx, srv.URL, "vk"); err != nil || !got.Empty() {
		t.Errorf("no budgets: %+v, %v", got, err)
	}
	var exhausted fakeGateway
	srv = exhausted.server(t, "/none", "", "/v1/usage", "Authorization", "Bearer ",
		`{"object":"key.usage","currency":"USD","budgets":[{"scope":"api_key","interval":"lifetime","enforcement":"hard","limit_usd":5,
		  "availability":{"status":"available","spent_usd":5,"reserved_usd":0,"remaining_usd":0}}]}`)
	got, err := Aixy{}.Read(ctx, srv.URL, "vk")
	if err != nil || !got.LimitReached || len(got.Windows) != 1 || got.Windows[0].Name != "key lifetime" || got.Windows[0].Duration != 0 {
		t.Errorf("exhausted lifetime budget: %+v, %v", got, err)
	}
}

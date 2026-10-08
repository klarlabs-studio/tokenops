package accounts

import (
	"context"
	"testing"
	"time"
)

func TestBifrostQuota(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/health", `{"status":"ok","components":{"db_pings":"ok"}}`, "/api/governance/virtual-keys/quota", "x-bf-vk", "",
		fixture(t, "bifrost"))
	ctx := context.Background()
	if !(Bifrost{}).Recognise(ctx, srv.URL) {
		t.Fatal("not recognised")
	}
	got, err := Bifrost{}.Read(ctx, srv.URL, "vk")
	if err != nil || len(got.Windows) != 2 || got.UsedUSD != 40 || got.LimitUSD != 100 || got.LimitReached {
		t.Fatalf("got %+v, %v", got, err)
	}
	if !got.Windows[0].ResetsAt.Equal(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("daily reset %v", got.Windows[0].ResetsAt)
	}
}

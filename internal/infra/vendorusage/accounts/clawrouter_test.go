package accounts

import (
	"context"
	"testing"
)

func TestClawRouterUsageInMicros(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/v1/health", `{"ok":true,"service":"clawrouter-edge"}`, "/v1/usage", "Authorization", "Bearer ", fixture(t, "clawrouter"))
	ctx := context.Background()
	if !(ClawRouter{}).Recognise(ctx, srv.URL) {
		t.Fatal("not recognised")
	}
	got, err := ClawRouter{}.Read(ctx, srv.URL, "vk")
	if err != nil || got.UsedUSD != 12.5 || got.LimitUSD != 50 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

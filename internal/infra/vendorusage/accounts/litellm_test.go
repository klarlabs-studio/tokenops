package accounts

import (
	"context"
	"testing"
)

func TestLiteLLMKeyInfo(t *testing.T) {
	var f fakeGateway
	srv := f.server(t, "/health/liveliness", `"I'm alive!"`, "/key/info", "Authorization", "Bearer ", fixture(t, "litellm"))
	ctx := context.Background()
	if !(LiteLLM{}).Recognise(ctx, srv.URL) || (Bifrost{}).Recognise(ctx, srv.URL) {
		t.Fatal("recognition")
	}
	got, err := LiteLLM{}.Read(ctx, srv.URL, "vk")
	if err != nil || got.UsedUSD != 12.5 || got.LimitUSD != 50 || len(got.Windows) != 1 || got.Windows[0].UsedPct != 25 || got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

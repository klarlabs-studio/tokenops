package accounts

import (
	"context"
	"testing"
)

func TestVercelCreditsAreStrings(t *testing.T) {
	srv := serve(t, "/v1/credits", "vk", fixture(t, "vercel"))
	defer srv.Close()
	got, err := Vercel{BaseURL: srv.URL}.Read(context.Background(), "vk")
	if err != nil || got.BalanceUSD != 95.5 || !got.HasBalance || got.Scope != "team" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

package accounts

import (
	"context"
	"testing"
)

func TestDeepInfraChecklist(t *testing.T) {
	srv := serve(t, "/payment/checklist", "dk", fixture(t, "deepinfra"))
	defer srv.Close()
	got, err := DeepInfra{BaseURL: srv.URL}.Read(context.Background(), "dk")
	if err != nil || got.UsedUSD != 3.25 || !got.HasUsed || got.BalanceUSD != 12.5 || !got.HasBalance || got.LimitUSD != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

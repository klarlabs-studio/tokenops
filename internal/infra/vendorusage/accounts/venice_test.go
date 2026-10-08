package accounts

import (
	"context"
	"errors"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// USD is a balance; DIEM left of the epoch's allocation is the share used.
func TestVeniceBalanceAndDiemEpoch(t *testing.T) {
	srv := serve(t, "/api/v1/billing/balance", "vk", fixture(t, "venice"))
	defer srv.Close()
	got, err := Venice{BaseURL: srv.URL}.Read(context.Background(), "vk")
	if err != nil || !got.HasBalance || got.BalanceUSD != 25 || got.LimitReached || len(got.Windows) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "epoch" || !approx(w.UsedPct, 9.5) || w.Duration != 0 || !w.ResetsAt.IsZero() {
		t.Errorf("window %+v", w)
	}
}

// Not staking: diem is null, no window. canConsume false is blocked.
func TestVeniceNotStakingAndBlocked(t *testing.T) {
	srv := serve(t, "/api/v1/billing/balance", "vk", `{"canConsume":false,"consumptionCurrency":null,"balances":{"diem":null,"usd":0},"diemEpochAllocation":0}`)
	defer srv.Close()
	got, err := Venice{BaseURL: srv.URL}.Read(context.Background(), "vk")
	if err != nil || !got.LimitReached || len(got.Windows) != 0 || !got.HasBalance || got.BalanceUSD != 0 {
		t.Errorf("%+v %v", got, err)
	}
}

func TestVeniceRefusedKey(t *testing.T) {
	srv := serve(t, "/api/v1/billing/balance", "vk", fixture(t, "venice"))
	defer srv.Close()
	if _, err := (Venice{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

func TestVeniceUnknownShape(t *testing.T) {
	srv := serve(t, "/api/v1/billing/balance", "k", `{"balance":3}`)
	defer srv.Close()
	if got, err := (Venice{BaseURL: srv.URL}).Read(context.Background(), "k"); err == nil {
		t.Errorf("read %+v", got)
	}
}

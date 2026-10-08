package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// The prepaid credit is a balance; the subscription's kWh allowance is a
// window that resets at the period's end.
func TestNeuralWattBalanceAndAllowanceWindow(t *testing.T) {
	srv := serve(t, "/v1/quota", "sk-nw", fixture(t, "neuralwatt"))
	defer srv.Close()
	got, err := NeuralWatt{BaseURL: srv.URL}.Read(context.Background(), "sk-nw")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 32.6774) || !got.Subscription || got.LimitReached || len(got.Windows) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	w := got.Windows[0]
	if w.Name != "month" || !approx(w.UsedPct, 69.5115) || w.Duration != 30*24*time.Hour ||
		!w.ResetsAt.Equal(time.Date(2026, 5, 11, 5, 5, 25, 0, time.UTC)) {
		t.Errorf("window %+v", w)
	}
}

// Without a subscription only the balance is read; a blocked key says so.
func TestNeuralWattPrepaidOnlyAndBlocked(t *testing.T) {
	srv := serve(t, "/v1/quota", "k", `{"balance":{"total_credits_usd":5,"credits_used_usd":5},"subscription":null,"key":{"allowance":{"blocked":true}}}`)
	defer srv.Close()
	got, err := NeuralWatt{BaseURL: srv.URL}.Read(context.Background(), "k")
	if err != nil || !got.HasBalance || got.BalanceUSD != 0 || got.Subscription || len(got.Windows) != 0 || !got.LimitReached {
		t.Errorf("%+v %v", got, err)
	}
}

func TestNeuralWattRefusedKey(t *testing.T) {
	srv := serve(t, "/v1/quota", "sk-nw", fixture(t, "neuralwatt"))
	defer srv.Close()
	if _, err := (NeuralWatt{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

func TestNeuralWattUnknownShape(t *testing.T) {
	for _, body := range []string{`{}`, `{"balance":{}}`} {
		srv := serve(t, "/v1/quota", "k", body)
		got, err := NeuralWatt{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if err == nil {
			t.Errorf("%s: read %+v", body, got)
		}
	}
}

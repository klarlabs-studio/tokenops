package accounts

import (
	"context"
	"errors"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestAtlasCloudReadsTheAvailableBalance(t *testing.T) {
	srv := serve(t, "/public/v1/balance", "apikey-ac", fixture(t, "atlascloud"))
	defer srv.Close()
	got, err := AtlasCloud{BaseURL: srv.URL}.Read(context.Background(), "apikey-ac")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 125.5) || got.Scope != "account" || got.HasUsed || got.Subscription {
		t.Errorf("%+v %v", got, err)
	}
}

func TestAtlasCloudRefusedKey(t *testing.T) {
	srv := serve(t, "/public/v1/balance", "apikey-ac", fixture(t, "atlascloud"))
	defer srv.Close()
	if _, err := (AtlasCloud{BaseURL: srv.URL}).Read(context.Background(), "ak_public"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

// A balance that is missing or not in dollars is an error, never $0.
func TestAtlasCloudUnknownShape(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"object":"balance","available":{"value":"5","currency":"eur"}}`,
		`{"object":"balance","available":{"value":"x","currency":"usd"}}`,
	} {
		srv := serve(t, "/public/v1/balance", "k", body)
		got, err := AtlasCloud{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if err == nil {
			t.Errorf("%s: read %+v", body, got)
		}
	}
}

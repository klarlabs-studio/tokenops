package accounts

import (
	"context"
	"errors"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// Poe's points stay points: no dollar balance is made up from them.
func TestPoeReadsThePointBalance(t *testing.T) {
	srv := serve(t, "/usage/current_balance", "poe-key", fixture(t, "poe"))
	defer srv.Close()
	got, err := Poe{BaseURL: srv.URL}.Read(context.Background(), "poe-key")
	if err != nil || !got.HasCredits || got.Credits != 295932027 || got.CreditsUnit != "points" || got.HasBalance || got.Empty() {
		t.Errorf("%+v %v", got, err)
	}
}

func TestPoeRefusedKey(t *testing.T) {
	srv := serve(t, "/usage/current_balance", "poe-key", fixture(t, "poe"))
	defer srv.Close()
	if _, err := (Poe{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

// An answer without the balance is an error, never zero points.
func TestPoeUnknownShape(t *testing.T) {
	srv := serve(t, "/usage/current_balance", "k", `{"balance":12}`)
	defer srv.Close()
	if got, err := (Poe{BaseURL: srv.URL}).Read(context.Background(), "k"); err == nil {
		t.Errorf("read %+v", got)
	}
}

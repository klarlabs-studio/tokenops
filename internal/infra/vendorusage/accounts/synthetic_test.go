package accounts

import (
	"context"
	"testing"
)

func TestSyntheticRequestQuota(t *testing.T) {
	srv := serve(t, "/v2/quotas", "sk", fixture(t, "synthetic"))
	defer srv.Close()
	got, err := Synthetic{BaseURL: srv.URL}.Read(context.Background(), "sk")
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 20) || got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

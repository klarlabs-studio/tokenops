package accounts

import (
	"context"
	"testing"
	"time"
)

func TestSyntheticRequestQuota(t *testing.T) {
	srv := serve(t, "/v2/quotas", "sk", fixture(t, "synthetic"))
	defer srv.Close()
	got, err := Synthetic{BaseURL: srv.URL}.Read(context.Background(), "sk")
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 20) || got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// The quota API has been seen returning renewAt where the docs say
// renewsAt; both spellings must yield the same reset.
func TestSyntheticAcceptsRenewAtSpelling(t *testing.T) {
	const body = `{"subscription":{"limit":135,"requests":27,"renewAt":"2025-09-21T14:36:14.288Z"}}`
	srv := serve(t, "/v2/quotas", "sk", body)
	defer srv.Close()
	got, err := Synthetic{BaseURL: srv.URL}.Read(context.Background(), "sk")
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	want := time.Date(2025, 9, 21, 14, 36, 14, 288000000, time.UTC)
	if !got.Windows[0].ResetsAt.Equal(want) {
		t.Fatalf("reset = %v, want %v", got.Windows[0].ResetsAt, want)
	}
}

// A quota with neither field still yields the window; only the reset is unknown.
func TestSyntheticWithoutRenewalFieldKeepsWindow(t *testing.T) {
	const body = `{"subscription":{"limit":135,"requests":27}}`
	srv := serve(t, "/v2/quotas", "sk", body)
	defer srv.Close()
	got, err := Synthetic{BaseURL: srv.URL}.Read(context.Background(), "sk")
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 20) || !got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

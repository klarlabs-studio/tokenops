package accounts

import (
	"context"
	"strconv"
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

// Current plans report a weekly credit limit, whose percentRemaining is in
// percentage points even below 1, and retire the subscription counter
// (CodexBar a20e95e696, SyntheticProviderTests).
func TestSyntheticPlanWeeklyLimit(t *testing.T) {
	const plan = `{
	  "subscription": { "limit": 500, "requests": 0, "renewsAt": "2026-10-31T15:15:05.743Z" },
	  "search": { "hourly": { "limit": 250, "requests": 0, "renewsAt": "2026-10-31T11:15:05.743Z" } },
	  "planWeeklyLimit": { "nextRegenAt": "2026-10-31T12:14:13.000Z", "percentRemaining": 99.47981765714286, "nextRegenPercent": 2 }
	}`
	for _, body := range []string{plan, `{"data":` + plan + `}`} {
		srv := serve(t, "/v2/quotas", "sk", body)
		got, err := Synthetic{BaseURL: srv.URL}.Read(context.Background(), "sk")
		srv.Close()
		if err != nil || !got.Subscription || len(got.Windows) != 1 {
			t.Fatalf("got %+v, %v", got, err)
		}
		w := got.Windows[0]
		if w.Name != "week" || w.Duration != 7*24*time.Hour || !approx(w.UsedPct, 0.52018234285714) ||
			!w.ResetsAt.Equal(time.Date(2026, 10, 31, 12, 14, 13, 0, time.UTC)) {
			t.Errorf("week %+v", w)
		}
	}
	for _, remaining := range []float64{0, 0.5, 1, 100} {
		body := `{"planWeeklyLimit":{"percentRemaining":` + strconv.FormatFloat(remaining, 'f', -1, 64) + `}}`
		srv := serve(t, "/v2/quotas", "sk", body)
		got, err := Synthetic{BaseURL: srv.URL}.Read(context.Background(), "sk")
		srv.Close()
		if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 100-remaining) {
			t.Errorf("%v: got %+v, %v", remaining, got, err)
		}
	}
}

// An answer with no quota at all is an error, never an account that
// reads as nothing.
func TestSyntheticNoQuotaIsAnError(t *testing.T) {
	for _, body := range []string{`{}`, `{"subscription":{"limit":0,"requests":0}}`, `{"planWeeklyLimit":{}}`} {
		srv := serve(t, "/v2/quotas", "sk", body)
		got, err := Synthetic{BaseURL: srv.URL}.Read(context.Background(), "sk")
		srv.Close()
		if err == nil {
			t.Errorf("%s: got %+v", body, got)
		}
	}
}

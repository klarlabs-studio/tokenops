package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var hfNow = func() time.Time { return time.Unix(1_755_000_000, 0) } // 2025-08-12

// hfServer answers the billing route for the month to date only.
func hfServer(t *testing.T, key, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.Header.Get("Authorization") != "Bearer "+key:
			w.WriteHeader(http.StatusForbidden)
		case r.URL.Path != "/api/settings/billing/usage-v2" || q.Get("startDate") != "1754006400" || q.Get("endDate") != "1755000000":
			w.WriteHeader(http.StatusNotFound)
		default:
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The charge is gross less the included amount, month to date.
func TestHuggingFaceMonthToDateCharge(t *testing.T) {
	srv := hfServer(t, "hf_x", fixture(t, "huggingface"))
	got, err := HuggingFace{BaseURL: srv.URL, Now: hfNow}.Read(context.Background(), "hf_x")
	if err != nil || !got.HasUsed || !approx(got.UsedUSD, 0.45) || got.LimitUSD != 0 || got.Subscription {
		t.Errorf("%+v %v", got, err)
	}
}

// A spending limit is the cap; an included amount above usage is $0.
func TestHuggingFaceLimitAndNoNegativeCharge(t *testing.T) {
	srv := hfServer(t, "k", `{"usage":{"inferenceProviders":{"usedNanoUsd":1000000000,"includedNanoUsd":0,"limitNanoUsd":4000000000}}}`)
	if got, err := (HuggingFace{BaseURL: srv.URL, Now: hfNow}).Read(context.Background(), "k"); err != nil || !approx(got.UsedUSD, 1) || got.LimitUSD != 4 {
		t.Errorf("%+v %v", got, err)
	}
	under := hfServer(t, "k", `{"usage":{"inferenceProviders":{"usedNanoUsd":100000000,"includedNanoUsd":2000000000}}}`)
	if got, err := (HuggingFace{BaseURL: under.URL, Now: hfNow}).Read(context.Background(), "k"); err != nil || !got.HasUsed || got.UsedUSD != 0 {
		t.Errorf("%+v %v", got, err)
	}
}

// A token without billing read is refused (403).
func TestHuggingFaceRefusedToken(t *testing.T) {
	srv := hfServer(t, "hf_x", fixture(t, "huggingface"))
	if _, err := (HuggingFace{BaseURL: srv.URL, Now: hfNow}).Read(context.Background(), "hf_fine_grained"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

func TestHuggingFaceUnknownShape(t *testing.T) {
	for _, body := range []string{`{}`, `{"usage":{}}`, `{"usage":{"inferenceProviders":{}}}`} {
		srv := hfServer(t, "k", body)
		if got, err := (HuggingFace{BaseURL: srv.URL, Now: hfNow}).Read(context.Background(), "k"); err == nil {
			t.Errorf("%s: read %+v", body, got)
		}
	}
}

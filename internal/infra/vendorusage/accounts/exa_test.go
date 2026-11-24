package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var exaNow = time.Date(2026, 10, 8, 12, 31, 36, 400e6, time.UTC)

func exaServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Header.Get("x-api-key") != "svc-key" || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/team-management/api-keys/key_123/usage" ||
			q.Get("start_date") != "2026-10-01T00:00:00.000Z" || q.Get("end_date") != "2026-10-08T12:31:36.000Z" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func TestExaReadsTheKeysSpendThisMonth(t *testing.T) {
	srv := exaServer(t, fixture(t, "exa"))
	defer srv.Close()
	got, err := Exa{BaseURL: srv.URL, Now: func() time.Time { return exaNow }}.Read(context.Background(), "key_123:svc-key")
	if err != nil || !got.HasUsed || !approx(got.UsedUSD, 45.67) || got.LimitUSD != 0 || got.HasBalance || got.Scope != "key" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestExaShapes(t *testing.T) {
	period := `"period":{"start":"2026-10-01T00:00:00Z","end":"2026-10-08T12:31:36Z"}`
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{`{"total_cost_usd":0,` + period + `}`, true},
		{`{"total_cost_usd":-1,` + period + `}`, false},
		{`{"total_cost_usd":"1",` + period + `}`, false},
		{`{"total_cost_usd":1,"period":{"start":"2026-09-01T00:00:00Z","end":"2026-10-08T12:31:36Z"}}`, false},
		{`{"total_cost_usd":1}`, false},
	} {
		srv := exaServer(t, tc.body)
		_, err := Exa{BaseURL: srv.URL, Now: func() time.Time { return exaNow }}.Read(context.Background(), "key_123:svc-key")
		srv.Close()
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.body, err)
		}
	}
	for _, cred := range []string{"svc-key", ":svc-key", "key_123:", "..:svc-key"} {
		if _, err := (Exa{}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) || !strings.Contains(err.Error(), "API_KEY_ID:SERVICE_KEY") {
			t.Errorf("%q: %v", cred, err)
		}
	}
}

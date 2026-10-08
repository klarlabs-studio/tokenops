package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// longCatServer serves LongCat's web API to the cookie "lc=ok": user
// answers user-current, summary the token-pack summary.
func longCatServer(t *testing.T, user, summary, legacy, fuel string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "lc=ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		answers := map[string]string{
			"GET /api/v1/user-current":                         user,
			"POST /api/pay/quota/metering/token-packs/summary": summary,
			"GET /api/lc-platform/v1/tokenUsage":               legacy,
			"GET /api/lc-platform/v1/pending-fuel-packages":    fuel,
		}
		body, ok := answers[r.Method+" "+r.URL.Path]
		if !ok || body == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const longCatUser = `{"code":0,"data":{"name":"me"}}`

func TestLongCatReadsTheTokenPack(t *testing.T) {
	srv := longCatServer(t, longCatUser, fixture(t, "longcat"), "",
		`{"code":0,"data":{"totalQuota":1000000,"list":[{"availableToken":400000,"expireTime":"2026-12-01 00:00:00"}]}}`)
	got, err := LongCat{BaseURL: srv.URL}.Read(context.Background(), "Cookie: lc=ok")
	if err != nil || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("%+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "tokens" || !approx(w.UsedPct, 25) || !w.ResetsAt.IsZero() {
		t.Errorf("window %+v", w)
	}
	if !got.HasCredits || got.Credits != 3750000+400000 || got.CreditsUnit != "tokens" || got.HasBalance {
		t.Errorf("balance %+v", got)
	}
}

// Without an active pack the legacy aggregate is the quota.
func TestLongCatLegacyUsage(t *testing.T) {
	srv := longCatServer(t, longCatUser, `{"code":0,"data":{"currentLot":null}}`,
		`{"code":0,"data":{"usage":{"totalToken":"2000","availableToken":"500"}}}`, "")
	got, err := LongCat{BaseURL: srv.URL}.Read(context.Background(), "lc=ok")
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 75) || got.Credits != 500 {
		t.Errorf("%+v, %v", got, err)
	}
}

func TestLongCatRefusals(t *testing.T) {
	srv := longCatServer(t, longCatUser, fixture(t, "longcat"), "", "")
	if _, err := (LongCat{BaseURL: srv.URL}).Read(context.Background(), "lc=expired"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused = %v", err)
	}
	inBody := longCatServer(t, `{"code":401,"message":"login required"}`, fixture(t, "longcat"), "", "")
	if _, err := (LongCat{BaseURL: inBody.URL}).Read(context.Background(), "lc=ok"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refusal in the body = %v", err)
	}
	if _, err := (LongCat{BaseURL: srv.URL}).Read(context.Background(), "bare-token"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("not a cookie = %v", err)
	}
}

func TestLongCatUnknownShape(t *testing.T) {
	srv := longCatServer(t, longCatUser, `{"code":0,"data":{}}`, `{"code":0,"data":{"usage":{}}}`, "")
	if got, err := (LongCat{BaseURL: srv.URL}).Read(context.Background(), "lc=ok"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("read %+v, %v", got, err)
	}
}

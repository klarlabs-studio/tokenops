package accounts

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func grokNow() time.Time { return time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC) }

func grokProxy(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gt" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/v1/billing" || r.URL.Query().Get("format") != "credits" || r.Header.Get("X-Xai-Token-Auth") != "xai-grok-cli" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGrokReadsTheCreditWindow(t *testing.T) {
	srv := grokProxy(t, routesOf(t, "grok")["/v1/billing"])
	got, err := Grok{BaseURL: srv.URL, Now: grokNow}.Read(context.Background(), "Bearer gt")
	if err != nil || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "week" || w.UsedPct != 12.5 || w.Duration != 7*24*time.Hour ||
		!w.ResetsAt.Equal(time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("window %+v", w)
	}
	if !got.HasBalance || !approx(got.BalanceUSD, 14.46) {
		t.Errorf("prepaid %+v", got)
	}
}

// Without a published share, the on-demand spend against its cap is the
// share; a balance alone is read; an empty config is not.
func TestGrokOnDemandAndPrepaid(t *testing.T) {
	got, err := Grok{BaseURL: grokProxy(t, `{"config":{"onDemandCap":{"val":1000.0},"onDemandUsed":{"val":250.5}}}`).URL, Now: grokNow}.Read(context.Background(), "gt")
	if err != nil || !approx(got.Windows[0].UsedPct, 25.05) || !got.Windows[0].ResetsAt.IsZero() || got.Windows[0].Name != "credits" {
		t.Errorf("on demand = %+v, %v", got, err)
	}
	got, err = Grok{BaseURL: grokProxy(t, `{"config":{"prepaidBalance":{}}}`).URL, Now: grokNow}.Read(context.Background(), "gt")
	if err != nil || !got.HasBalance || got.BalanceUSD != 0 || got.Subscription {
		t.Errorf("empty prepaid = %+v, %v", got, err)
	}
	for _, body := range []string{`{"config":{}}`, `{}`, `{"config":{"prepaidBalance":{"val":1.5}}}`, `{"config":{"prepaidBalance":{"val":-1}}}`} {
		if got, err := (Grok{BaseURL: grokProxy(t, body).URL, Now: grokNow}).Read(context.Background(), "gt"); err == nil {
			t.Errorf("%s = %+v", body, got)
		}
	}
}

func TestGrokRefusalsAndSkips(t *testing.T) {
	srv := grokProxy(t, routesOf(t, "grok")["/v1/billing"])
	if _, err := (Grok{BaseURL: srv.URL}).Read(context.Background(), "expired"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	if _, err := (Grok{BaseURL: srv.URL}).Read(context.Background(), "xai-management"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("an xAI key = %v", err)
	}
	if _, err := (Grok{BaseURL: srv.URL}).Read(context.Background(), "sso=s"); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("key reader given a session = %v", err)
	}
	if _, err := (GrokWeb{BaseURL: srv.URL}).Read(context.Background(), "gt"); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("session reader given a key = %v", err)
	}
}

// grokWeb is grok.com's billing call: a gRPC-web answer for the sso
// cookie "s", with status in a trailer frame.
func grokWeb(t *testing.T, payload []byte, status string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "sso=s") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/grok_api_v2.GrokBuildBilling/GetGrokCreditsConfig" || r.Header.Get("Content-Type") != "application/grpc-web+proto" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write(grpcFrame(0, payload))
		_, _ = w.Write(grpcFrame(0x80, []byte("grpc-status:"+status+"\r\n")))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func grpcFrame(flag byte, b []byte) []byte {
	out := make([]byte, 5, 5+len(b))
	out[0] = flag
	binary.BigEndian.PutUint32(out[1:], uint32(len(b))) // #nosec G115 -- a test payload
	return append(out, b...)
}

func grokFixtureBytes(t *testing.T, key string) []byte {
	t.Helper()
	b, err := hex.DecodeString(routesOf(t, "grok")[key])
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGrokWebReadsTheCapturedAnswer(t *testing.T) {
	srv := grokWeb(t, grokFixtureBytes(t, "grpc-web"), "0")
	now := time.Unix(1781000000, 0)
	got, err := GrokWeb{BaseURL: srv.URL, Now: func() time.Time { return now }}.Read(context.Background(), "sso=s; other=o")
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; !approx(w.UsedPct, 1.222) || w.ResetsAt.Unix() != 1782864000 {
		t.Errorf("window %+v", w)
	}
}

// An unused period omits the share: it reads as zero with its reset.
func TestGrokWebUnusedPeriod(t *testing.T) {
	raw := grokFixtureBytes(t, "grpc-web-unused")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) }))
	defer srv.Close()
	got, err := GrokWeb{BaseURL: srv.URL, Now: func() time.Time { return time.Unix(1779000000, 0) }}.Read(context.Background(), "sso=s")
	if err != nil || got.Windows[0].UsedPct != 0 || got.Windows[0].ResetsAt.Unix() != 1780272000 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestGrokWebRefusalsAndUnknown(t *testing.T) {
	srv := grokWeb(t, grokFixtureBytes(t, "grpc-web"), "16")
	if _, err := (GrokWeb{BaseURL: srv.URL}).Read(context.Background(), "sso=s"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("grpc 16 = %v", err)
	}
	if _, err := (GrokWeb{BaseURL: srv.URL}).Read(context.Background(), "sso=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	if _, err := (GrokWeb{BaseURL: srv.URL}).Read(context.Background(), "theme=dark"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("no sso cookie = %v", err)
	}
	// A reset-only payload is not a reading.
	resetOnly := grokWeb(t, []byte{0x0a, 0x08, 0x2a, 0x06, 0x08, 0x80, 0xb1, 0x91, 0xd2, 0x06}, "0")
	if _, err := (GrokWeb{BaseURL: resetOnly.URL}).Read(context.Background(), "sso=s"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("reset only = %v", err)
	}
	html := grokWeb(t, []byte("<html>"), "0")
	if _, err := (GrokWeb{BaseURL: html.URL}).Read(context.Background(), "sso=s"); err == nil {
		t.Error("an HTML payload was read")
	}
}

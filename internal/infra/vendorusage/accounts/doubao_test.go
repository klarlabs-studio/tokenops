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

var doubaoNow = func() time.Time { return time.Unix(1_781_654_400, 0) }

// The signature matches an independent implementation of the scheme
// CodexBar's DoubaoVolcengineSigner uses, for the request it sends.
func TestVolcengineSignatureKnownAnswer(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://open.volcengineapi.com/?Action=GetCodingPlanUsage&Version=2024-01-01", http.NoBody)
	volcengineSign(req, nil, "AKLTTEST", "secret", "cn-beijing", doubaoNow())
	want := "HMAC-SHA256 Credential=AKLTTEST/20260617/cn-beijing/ark/request, SignedHeaders=content-type;host;x-content-sha256;x-date, " +
		"Signature=220f360943ab513c639db31ee72aeee7fa8b915812cde28ce104d6496b0bd24d"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization\n got %s\nwant %s", got, want)
	}
	if req.Header.Get("X-Date") != "20260617T000000Z" || req.Header.Get("X-Content-Sha256") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("headers %v", req.Header)
	}
}

// doubaoServer answers each action with its body, refusing any request
// not signed with access key AK.
func doubaoServer(t *testing.T, answers map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Authorization"), "HMAC-SHA256 Credential=AK/") || r.URL.Query().Get("Version") != "2024-01-01" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, ok := answers[r.URL.Query().Get("Action")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The Coding Plan's session, weekly and monthly levels are the 5-hour,
// week and month windows, Percent already a share used.
func TestDoubaoCodingPlanWindows(t *testing.T) {
	srv := doubaoServer(t, map[string]string{"GetCodingPlanUsage": fixture(t, "doubao")})
	got, err := Doubao{BaseURL: srv.URL, Now: doubaoNow}.Read(context.Background(), "AK:SK")
	if err != nil || !got.Subscription || len(got.Windows) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	want := []struct {
		name  string
		pct   float64
		reset int64
	}{{"5h", 0.116, 1782226478}, {"week", 3.182143, 1782662400}, {"month", 7.5730535, 1782403199}}
	for i, w := range want {
		g := got.Windows[i]
		if g.Name != w.name || !approx(g.UsedPct, w.pct) || g.ResetsAt.Unix() != w.reset {
			t.Errorf("window %d = %+v, want %+v", i, g, w)
		}
	}
}

// With no Coding Plan windows the Agent Plan is read; ResetTime is in
// milliseconds.
func TestDoubaoAgentPlanFallback(t *testing.T) {
	srv := doubaoServer(t, map[string]string{
		"GetCodingPlanUsage": `{"Result":{"Status":"Expired"}}`,
		"GetAFPUsage":        `{"Result":{"AFPFiveHour":{"Quota":200,"Used":50,"ResetTime":1782226478000},"AFPWeekly":{"Quota":0,"Used":0}}}`,
	})
	got, err := Doubao{BaseURL: srv.URL, Now: doubaoNow}.Read(context.Background(), "AK:SK:cn-beijing")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != "5h" || !approx(got.Windows[0].UsedPct, 25) || got.Windows[0].ResetsAt.Unix() != 1782226478 {
		t.Errorf("%+v %v", got, err)
	}
	// No plan at all reads as nothing to store.
	none := doubaoServer(t, map[string]string{"GetCodingPlanUsage": `{"Result":{"Status":"Expired"}}`})
	if got, err := (Doubao{BaseURL: none.URL, Now: doubaoNow}).Read(context.Background(), "AK:SK"); err != nil || !got.Empty() {
		t.Errorf("no plan: %+v %v", got, err)
	}
}

// A refused pair, and an Ark API key (not an AccessKey pair), are refused
// without a chat completion being sent.
func TestDoubaoRefusals(t *testing.T) {
	srv := doubaoServer(t, map[string]string{"GetCodingPlanUsage": fixture(t, "doubao")})
	for _, cred := range []string{"OTHER:SK", "ark-api-key-123", "AK:", "a:b:c:d"} {
		if _, err := (Doubao{BaseURL: srv.URL, Now: doubaoNow}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: err %v", cred, err)
		}
	}
}

func TestDoubaoUnknownShape(t *testing.T) {
	srv := doubaoServer(t, map[string]string{"GetCodingPlanUsage": `[]`})
	if got, err := (Doubao{BaseURL: srv.URL, Now: doubaoNow}).Read(context.Background(), "AK:SK"); err == nil {
		t.Errorf("read %+v", got)
	}
}

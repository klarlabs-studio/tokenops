package accounts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const aliSession = "login_aliyunid_ticket=tkt; login_aliyunid_csrf=csrf1; cna=anon"

func aliConsole(t *testing.T, refuse int, quota string) *webServer {
	return newWebServer(t, "login_aliyunid_ticket=tkt", refuse, map[string]string{
		"/ap-southeast-1/": `<script>window.ALIYUN_CONSOLE_CONFIG = { SEC_TOKEN: "sec-1" };</script>`,
		"/cn-beijing/":     `<script>window.ALIYUN_CONSOLE_CONFIG = { SEC_TOKEN: "sec-2" };</script>`,
		"/data/api.json":   quota,
	})
}

func checkAlibabaWindows(t *testing.T, got usage.Reading) {
	t.Helper()
	if !got.Subscription || len(got.Windows) != 3 {
		t.Fatalf("got %+v", got)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 5.2) || !w.ResetsAt.Equal(time.Unix(1700000300, 0)) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 16) {
		t.Errorf("week %+v", w)
	}
	if w := got.Windows[2]; w.Name != "month" || !approx(w.UsedPct, 6) || w.ResetsAt.IsZero() {
		t.Errorf("month %+v", w)
	}
}

func TestAlibabaWebReadsTheConsoleWithItsToken(t *testing.T) {
	srv := aliConsole(t, http.StatusUnauthorized, fixture(t, "alibaba"))
	got, err := AlibabaWeb{BaseURLs: []string{srv.URL}}.Read(context.Background(), aliSession)
	if err != nil {
		t.Fatal(err)
	}
	checkAlibabaWindows(t, got)
	last := srv.bodies[len(srv.bodies)-1]
	for _, want := range []string{"sec_token=sec-1", "region=ap-southeast-1", "X-Anonymous-Id", "sfm_codingplan_public_intl"} {
		if !strings.Contains(last, want) {
			t.Errorf("quota request misses %q", want)
		}
	}
}

// An expired session is sent to the sign-in page, or answered with
// ConsoleNeedLogin; on either console it is a refusal.
func TestAlibabaWebExpiredSession(t *testing.T) {
	intl, cn := aliConsole(t, http.StatusFound, "{}"), aliConsole(t, http.StatusFound, "{}")
	if _, err := (AlibabaWeb{BaseURLs: []string{intl.URL, cn.URL}}).Read(context.Background(), "login_aliyunid_ticket=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("redirected session = %v", err)
	}
	if cn.requests() == 0 {
		t.Error("the mainland console was not tried")
	}
	login := aliConsole(t, http.StatusUnauthorized, `{"code":"ConsoleNeedLogin","message":"You need to log in.","successResponse":false}`)
	if _, err := (AlibabaWeb{BaseURLs: []string{login.URL, login.URL}}).Read(context.Background(), aliSession); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("ConsoleNeedLogin = %v", err)
	}
}

// A session for the mainland console finds no plan on the international
// one and is read there.
func TestAlibabaWebFallsBackToTheMainland(t *testing.T) {
	intl := aliConsole(t, http.StatusUnauthorized, `{"data":{"codingPlanInstanceInfos":[]},"code":"200"}`)
	cn := aliConsole(t, http.StatusUnauthorized, fixture(t, "alibaba"))
	got, err := AlibabaWeb{BaseURLs: []string{intl.URL, cn.URL}}.Read(context.Background(), aliSession)
	if err != nil {
		t.Fatal(err)
	}
	checkAlibabaWindows(t, got)
	if !strings.Contains(cn.bodies[len(cn.bodies)-1], "sec_token=sec-2") {
		t.Error("the mainland console's token was not used")
	}
}

func TestAlibabaKeyReadsWithTheCodingPlanKey(t *testing.T) {
	srv := serve(t, "/data/api.json", "sk-sp-1", fixture(t, "alibaba"))
	defer srv.Close()
	got, err := Alibaba{BaseURLs: []string{srv.URL}}.Read(context.Background(), "sk-sp-1")
	if err != nil {
		t.Fatal(err)
	}
	checkAlibabaWindows(t, got)
	if _, err := (Alibaba{BaseURLs: []string{srv.URL, srv.URL}}).Read(context.Background(), "sk-bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused key = %v", err)
	}
	// Each reader declines the other's credential without a call.
	if _, err := (Alibaba{}).Read(context.Background(), aliSession); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("a session given to the key reader = %v", err)
	}
	if _, err := (AlibabaWeb{}).Read(context.Background(), "sk-sp-1"); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("a key given to the session reader = %v", err)
	}
}

// Where the console wants a signed-in session, the key is not refused:
// it cannot read the quota there, and the error says what can.
func TestAlibabaKeyWhereTheConsoleWantsASession(t *testing.T) {
	srv := serve(t, "/data/api.json", "sk-sp-1", `{"code":"ConsoleNeedLogin","message":"You need to log in."}`)
	defer srv.Close()
	_, err := Alibaba{BaseURLs: []string{srv.URL}}.Read(context.Background(), "sk-sp-1")
	if err == nil || errors.Is(err, usage.ErrAuth) || !strings.Contains(err.Error(), "setup alibaba") {
		t.Errorf("err = %v", err)
	}
}

func TestAlibabaUnknownShape(t *testing.T) {
	srv := serve(t, "/data/api.json", "sk-sp-1", `{"unexpected":true}`)
	defer srv.Close()
	if _, err := (Alibaba{BaseURLs: []string{srv.URL, srv.URL}}).Read(context.Background(), "sk-sp-1"); err == nil {
		t.Error("an unknown shape was read")
	}
	active := serve(t, "/data/api.json", "sk-sp-1", `{"data":{"codingPlanInstanceInfos":[{"planName":"Lite","status":"VALID"}]}}`)
	defer active.Close()
	got, err := Alibaba{BaseURLs: []string{active.URL}}.Read(context.Background(), "sk-sp-1")
	if err != nil || !got.Empty() {
		t.Errorf("an active plan without counters = %+v, %v", got, err)
	}
}

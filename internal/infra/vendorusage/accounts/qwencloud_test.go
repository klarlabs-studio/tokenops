package accounts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const qwenSession = "login_qwencloud_ticket=qt; cna=anon"

func qwenServer(t *testing.T, refuse int, usageBody string) *webServer {
	return newWebServer(t, "login_qwencloud_ticket=qt", refuse, map[string]string{
		"/tool/user/info.json": fixturePart(t, "qwencloud", "user_info"),
		tokenPlanUsageRoute:    usageBody,
	})
}

// The token comes from the console's user info (embedded JSON) when the
// page has none, and the three windows are read.
func TestQwenCloudReadsTheIndividualPlan(t *testing.T) {
	srv := qwenServer(t, http.StatusUnauthorized, fixturePart(t, "qwencloud", "usage"))
	got, err := QwenCloud{Home: srv.URL, Data: srv.URL}.Read(context.Background(), qwenSession)
	if err != nil || len(got.Windows) != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
	for i, want := range []struct {
		name string
		pct  float64
	}{{"5h", 25}, {"week", 40}, {"month", 10}} {
		if w := got.Windows[i]; w.Name != want.name || !approx(w.UsedPct, want.pct) || w.ResetsAt.IsZero() {
			t.Errorf("window %d %+v", i, w)
		}
	}
	last := srv.bodies[len(srv.bodies)-1]
	if !strings.Contains(last, "sec_token=qwen-sec") || !strings.Contains(last, "region=ap-southeast-1") {
		t.Errorf("usage request misses the token or region")
	}
}

func TestQwenCloudRefusals(t *testing.T) {
	for name, body := range map[string]string{
		"login":     fixturePart(t, "qwencloud", "login_required"),
		"forbidden": fixturePart(t, "qwencloud", "forbidden"),
	} {
		srv := qwenServer(t, http.StatusUnauthorized, body)
		if _, err := (QwenCloud{Home: srv.URL, Data: srv.URL}).Read(context.Background(), qwenSession); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	srv := qwenServer(t, http.StatusFound, "{}")
	if _, err := (QwenCloud{Home: srv.URL, Data: srv.URL}).Read(context.Background(), "login_qwencloud_ticket=expired"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired session = %v", err)
	}
	if _, err := (QwenCloud{}).Read(context.Background(), "sk-not-a-session"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a key = %v", err)
	}
}

func TestQwenCloudWithoutAPlan(t *testing.T) {
	srv := qwenServer(t, http.StatusUnauthorized, `{"code":"200","successResponse":true,"data":{"success":true,"data":{}}}`)
	got, err := QwenCloud{Home: srv.URL, Data: srv.URL}.Read(context.Background(), qwenSession)
	if err != nil || !got.Empty() {
		t.Errorf("no plan = %+v, %v", got, err)
	}
}

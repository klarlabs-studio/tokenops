package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// fixturePart is one named answer of a provider's fixture file, for a
// vendor whose reading takes several calls.
func fixturePart(t *testing.T, id, part string) string {
	t.Helper()
	var parts map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fixture(t, id)), &parts); err != nil {
		t.Fatal(err)
	}
	raw, ok := parts[part]
	if !ok {
		t.Fatalf("%s.json has no %q", id, part)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

const (
	tokenPlanUsageRoute = "/data/api.json#" + tokenPlanUsageAPI
	tokenPlanTeamRoute  = "/data/api.json#GetSubscriptionSummary"
)

func tokenPlanConsoleServer(t *testing.T, refuse int, usageBody, teamBody string) *webServer {
	return newWebServer(t, "login_aliyunid_ticket=tkt", refuse, map[string]string{
		"/ap-southeast-1/":  `<script>SEC_TOKEN: "sec-1"</script>`,
		"/cn-beijing":       `<script>SEC_TOKEN: "sec-2"</script>`,
		tokenPlanUsageRoute: usageBody,
		tokenPlanTeamRoute:  teamBody,
	})
}

func TestAlibabaTokenPlanReadsPersonalWindows(t *testing.T) {
	srv := tokenPlanConsoleServer(t, http.StatusUnauthorized, fixturePart(t, "alibabatokenplan", "personal_usage"), "{}")
	got, err := AlibabaTokenPlan{BaseURLs: []string{srv.URL}}.Read(context.Background(), aliSession)
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 0.0997) || !w.ResetsAt.Equal(time.UnixMilli(1784813220000)) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || w.Duration != 7*24*time.Hour {
		t.Errorf("week %+v", w)
	}
	if !strings.Contains(srv.bodies[len(srv.bodies)-1], "sec_token=sec-1") {
		t.Error("the console's security token was not sent")
	}
}

// A Team plan has no personal windows: its credit pool is read instead.
func TestAlibabaTokenPlanReadsTheTeamPool(t *testing.T) {
	srv := tokenPlanConsoleServer(t, http.StatusUnauthorized, fixturePart(t, "alibabatokenplan", "no_subscription"), fixturePart(t, "alibabatokenplan", "team_summary"))
	got, err := AlibabaTokenPlan{BaseURLs: []string{srv.URL}}.Read(context.Background(), aliSession)
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "credits" || !approx(w.UsedPct, 12.5) || !w.ResetsAt.Equal(time.UnixMilli(1701000000000)) {
		t.Errorf("credits %+v", w)
	}
}

// An expired session is refused by both consoles; one that answers with
// no plan anywhere reads as nothing to store.
func TestAlibabaTokenPlanExpiredOrEmpty(t *testing.T) {
	login := fixturePart(t, "alibabatokenplan", "login_required")
	intl, cn := tokenPlanConsoleServer(t, http.StatusUnauthorized, login, login), tokenPlanConsoleServer(t, http.StatusFound, login, login)
	if _, err := (AlibabaTokenPlan{BaseURLs: []string{intl.URL, cn.URL}}).Read(context.Background(), aliSession); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("ConsoleNeedLogin = %v", err)
	}
	if _, err := (AlibabaTokenPlan{BaseURLs: []string{intl.URL, cn.URL}}).Read(context.Background(), "login_aliyunid_ticket=expired"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused session = %v", err)
	}
	none := fixturePart(t, "alibabatokenplan", "no_subscription")
	empty := tokenPlanConsoleServer(t, http.StatusUnauthorized, none, none)
	got, err := AlibabaTokenPlan{BaseURLs: []string{empty.URL, empty.URL}}.Read(context.Background(), aliSession)
	if err != nil || !got.Empty() {
		t.Errorf("no plan = %+v, %v", got, err)
	}
	odd := tokenPlanConsoleServer(t, http.StatusUnauthorized, `{"unexpected":true}`, `{"unexpected":true}`)
	if _, err := (AlibabaTokenPlan{BaseURLs: []string{odd.URL, odd.URL}}).Read(context.Background(), aliSession); err == nil {
		t.Error("an unknown shape was read")
	}
}

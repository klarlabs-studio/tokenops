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

const workBuddyCookie = "session=wb; locale=zh"

func workBuddyNow() time.Time { return time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC) }

func TestWorkBuddyReadsTheCredits(t *testing.T) {
	srv := newWebServer(t, "session=wb", http.StatusUnauthorized, routesOf(t, "workbuddy"))
	got, err := WorkBuddy{BaseURL: srv.URL, Now: workBuddyNow, Chrome: 141}.Read(context.Background(), workBuddyCookie)
	if err != nil || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	// 50 of 500 credits; the cycle ends 2026-10-31 23:59:59 China time.
	if w := got.Windows[0]; w.Name != "credits" || !approx(w.UsedPct, 10) || w.ResetsAt.Unix() != 1793462400 {
		t.Errorf("window %+v (%d)", w, w.ResetsAt.Unix())
	}
	// The listings name the package codes; the paid one (no route) is skipped.
	if !strings.Contains(strings.Join(srv.bodies, ""), `"PackageCodes":["TCACA_code_001_PqouKr6QWV"`) {
		t.Errorf("free listing body missing its codes: %v", srv.bodies)
	}
}

// The session is bound to Chrome's User-Agent: a refusal is retried once
// as the previous Chrome version.
func TestWorkBuddyRetriesThePreviousChrome(t *testing.T) {
	routes := routesOf(t, "workbuddy")
	var agents []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.Header.Get("User-Agent"))
		if !strings.Contains(r.Header.Get("User-Agent"), "Chrome/140.") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(routes[r.URL.Path]))
	}))
	defer srv.Close()
	got, err := WorkBuddy{BaseURL: srv.URL, Now: workBuddyNow, Chrome: 141}.Read(context.Background(), workBuddyCookie)
	if err != nil || len(got.Windows) != 1 || !strings.Contains(agents[0], "Chrome/141.") {
		t.Fatalf("got %+v, %v, agents %v", got, err, agents)
	}
	if _, err := (WorkBuddy{BaseURL: srv.URL, Now: workBuddyNow, Chrome: 150}).Read(context.Background(), workBuddyCookie); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("both agents refused = %v, want ErrAuth", err)
	}
}

func TestWorkBuddyNoCreditsAndOtherUnits(t *testing.T) {
	routes := routesOf(t, "workbuddy")
	routes["/billing/meter/get-user-resource-summary"] = `{"code":0,"data":{"Packages":[{"CapacityUnit":"tokens","CycleTotalCapacity":"9","CycleRemainCapacity":"1"}]}}`
	srv := newWebServer(t, "session=wb", http.StatusUnauthorized, routes)
	got, err := WorkBuddy{BaseURL: srv.URL, Now: workBuddyNow, Chrome: 141}.Read(context.Background(), workBuddyCookie)
	if err != nil || !got.Empty() {
		t.Errorf("no credit package = %+v, %v", got, err)
	}
}

func TestWorkBuddyRefusedOrUnknown(t *testing.T) {
	srv := newWebServer(t, "session=wb", http.StatusUnauthorized, routesOf(t, "workbuddy"))
	if _, err := (WorkBuddy{BaseURL: srv.URL, Chrome: 141}).Read(context.Background(), "session=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	for _, body := range []string{
		`{"code":10001,"msg":"bad"}`,
		`{"msg":"OK"}`,
		`{"code":0,"data":{}}`,
		`{"code":0,"data":{"Packages":[{"CapacityUnit":"credits","CycleTotalCapacity":"-1","CycleRemainCapacity":"0"}]}}`,
		`{"code":0,"data":{"Packages":[{"CapacityUnit":"credits","CycleTotalCapacity":"1e3","CycleRemainCapacity":"0"}]}}`,
	} {
		srv := newWebServer(t, "session=wb", http.StatusUnauthorized, map[string]string{"/billing/meter/get-user-resource-summary": body})
		if _, err := (WorkBuddy{BaseURL: srv.URL, Chrome: 141}).Read(context.Background(), workBuddyCookie); err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s = %v, want a parse error", body, err)
		}
	}
}

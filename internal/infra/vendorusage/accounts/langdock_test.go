package accounts

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const langdockPath = "/api/trpc/usageSettings.getPersonalUsage"

func langdockServer(t *testing.T, body string) *webServer {
	t.Helper()
	return newWebServer(t, "auth_token=synthetic-account-a", http.StatusUnauthorized, map[string]string{langdockPath: body})
}

func TestLangdockReadsTheIncludedLimits(t *testing.T) {
	srv := langdockServer(t, fixture(t, "langdock"))
	got, err := Langdock{BaseURL: srv.URL}.Read(context.Background(), "auth_token=synthetic-account-a")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || w.UsedPct != 12.5 || w.Duration != 5*time.Hour ||
		!w.ResetsAt.Equal(time.Date(2026, 9, 25, 12, 0, 0, 123e6, time.UTC)) {
		t.Errorf("session %+v", w)
	}
	// Langdock lets the week run over; the share is held at 100.
	if w := got.Windows[1]; w.Name != "week" || w.UsedPct != 100 || !w.ResetsAt.Equal(time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("week %+v", w)
	}
}

func TestLangdockWithoutLimitsOrSessionLimit(t *testing.T) {
	for _, body := range []string{
		`[{"result":{"data":{"json":{"hasIncludedUsageLimits":false,"planUsage":null}}}}]`,
		`[{"result":{"data":{"json":{"hasIncludedUsageLimits":true,"planUsage":null}}}}]`,
	} {
		got, err := Langdock{BaseURL: langdockServer(t, body).URL}.Read(context.Background(), "auth_token=synthetic-account-a")
		if err != nil || !got.Empty() {
			t.Errorf("%s = %+v, %v", body, got, err)
		}
	}
	body := `[{"result":{"data":{"json":{"planUsage":{"sessionUsageLimitsEnabled":false,"sessionUsagePercent":3,"weeklyUsagePercent":7,"weeklyResetsAt":null}}}}}]`
	got, err := Langdock{BaseURL: langdockServer(t, body).URL}.Read(context.Background(), "auth_token=synthetic-account-a")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != "week" || !got.Windows[0].ResetsAt.IsZero() {
		t.Errorf("week only = %+v, %v", got, err)
	}
}

func TestLangdockRefusals(t *testing.T) {
	srv := langdockServer(t, fixture(t, "langdock"))
	if _, err := (Langdock{BaseURL: srv.URL}).Read(context.Background(), "auth_token=signed-out"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	body := langdockServer(t, `[{"error":{"json":{"data":{"code":"UNAUTHORIZED"}}}}]`)
	if _, err := (Langdock{BaseURL: body.URL}).Read(context.Background(), "auth_token=synthetic-account-a"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("tRPC refusal = %v", err)
	}
	if _, err := (Langdock{BaseURL: srv.URL}).Read(context.Background(), "session=x"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("no auth_token = %v", err)
	}
}

func TestLangdockUnknownShape(t *testing.T) {
	for _, body := range []string{
		`[]`, `[{},{}]`, `{"data":null}`, `[{}]`,
		`[{"error":{"json":{"data":{"code":7}}}}]`,
		`[{"error":{"json":{"data":{"code":"BAD_REQUEST"}}}}]`,
		`[{"result":{"data":{"json":{"planUsage":{"sessionUsageLimitsEnabled":true,"sessionUsagePercent":"12","weeklyUsagePercent":1}}}}}]`,
		`[{"result":{"data":{"json":{"planUsage":{"sessionUsageLimitsEnabled":false,"weeklyUsagePercent":1,"weeklyResetsAt":"soon"}}}}}]`,
		`[{"result":{"data":{"json":{"hasIncludedUsageLimits":"yes"}}}}]`,
	} {
		got, err := Langdock{BaseURL: langdockServer(t, body).URL}.Read(context.Background(), "auth_token=synthetic-account-a")
		if err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s = %+v, %v; want a parse error", body, got, err)
		}
	}
}

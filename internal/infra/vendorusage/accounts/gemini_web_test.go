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

func geminiWebServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "SID=fixture; __Secure-1PSID=x" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/_/BardChatUi/graphql/schemas/GEMINI_WEB_GRAPHQL/executeQuery" ||
			r.URL.Query().Get("query_signature") != geminiUsageSignature || r.URL.Query().Get("variables") != geminiUsageVariables {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func TestGeminiWebReadsTheAppsWindows(t *testing.T) {
	srv := geminiWebServer(t, ")]}'\n"+fixture(t, "gemini"))
	defer srv.Close()
	got, err := GeminiWeb{BaseURL: srv.URL}.Read(context.Background(), "SID=fixture; __Secure-1PSID=x")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	five, week := got.Windows[0], got.Windows[1]
	if five.Name != "5h" || !approx(five.UsedPct, 24) || !five.ResetsAt.Equal(time.Date(2026, 10, 15, 5, 43, 53, 558200000, time.UTC)) ||
		week.Name != "week" || !approx(week.UsedPct, 1.786011) || week.Duration != 7*24*time.Hour {
		t.Errorf("%+v", got.Windows)
	}
}

func TestGeminiWebShapes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok, auth   bool
		windows    int
	}{
		// A reported zero stays zero; a missing share stays absent.
		{"zero", `[{"data":{"viewer":{"rpcs":{"getUsageInfo":{"pools":[{"poolType":"GXU_FIVE_HOURLY","percentageUsed":null},{"poolType":"GXU_WEEKLY","percentageUsed":0,"gxu":{"windowRefreshesAt":"unknown"}}]}}}}}]`, true, false, 1},
		{"signed out", `[{"data":{"viewer":null}}]`, false, true, 0},
		{"sign-in page", `<html>sign in</html>`, false, true, 0},
		{"no windows", `[{"data":{"viewer":{"rpcs":{"getUsageInfo":{"pools":[]}}}}}]`, false, false, 0},
		{"errors only", `[{"data":{},"errors":[{"message":"private-response"}]}]`, false, false, 0},
	} {
		srv := geminiWebServer(t, tc.body)
		got, err := GeminiWeb{BaseURL: srv.URL}.Read(context.Background(), "SID=fixture; __Secure-1PSID=x")
		srv.Close()
		if (err == nil) != tc.ok || errors.Is(err, usage.ErrAuth) != tc.auth || (tc.ok && len(got.Windows) != tc.windows) {
			t.Errorf("%s: %+v %v", tc.name, got, err)
		}
	}
}

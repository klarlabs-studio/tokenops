package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// aiandServer answers /logs: first when no cursor is sent, then next for
// the cursor first named.
func aiandServer(t *testing.T, key, first, next string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.Header.Get("Authorization") != "Bearer "+key:
			w.WriteHeader(http.StatusUnauthorized)
		case status != 0:
			w.WriteHeader(status)
		case r.URL.Path != "/logs" || q.Get("range") != "30days":
			w.WriteHeader(http.StatusNotFound)
		case q.Get("after_id") == "":
			_, _ = w.Write([]byte(first))
		case q.Get("after_id") == "912bf992-0000-4000-8000-000000000002" && q.Get("after") == "2026-07-17 10:24:30.094374+00":
			_, _ = w.Write([]byte(next))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func aiandPagesFixture(t *testing.T) (string, string) {
	t.Helper()
	var f struct {
		First json.RawMessage `json:"first_page"`
		Final json.RawMessage `json:"final_page"`
	}
	if err := json.Unmarshal([]byte(fixture(t, "aiand")), &f); err != nil {
		t.Fatal(err)
	}
	return string(f.First), string(f.Final)
}

// Every page is followed, both cursors together, and the costs add up
// exactly; a failed request with no cost is skipped.
func TestAiAndSumsThirtyDaysOfLogs(t *testing.T) {
	first, final := aiandPagesFixture(t)
	srv := aiandServer(t, "sk-aa", first, final, 0)
	got, err := AiAnd{BaseURL: srv.URL}.Read(context.Background(), "sk-aa")
	if err != nil || !got.HasUsed || !approx(got.UsedUSD, 20.62344) || got.LimitUSD != 0 || got.HasBalance || got.Subscription || got.UsedPeriod != 30*24*time.Hour {
		t.Errorf("%+v %v", got, err)
	}
}

func TestAiAndRefusedKey(t *testing.T) {
	first, final := aiandPagesFixture(t)
	srv := aiandServer(t, "sk-aa", first, final, 0)
	if _, err := (AiAnd{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

// 402 is ai& saying the organisation is out of credit.
func TestAiAndOutOfCredit(t *testing.T) {
	srv := aiandServer(t, "k", "", "", http.StatusPaymentRequired)
	got, err := AiAnd{BaseURL: srv.URL}.Read(context.Background(), "k")
	if err != nil || !got.LimitReached || got.HasUsed {
		t.Errorf("%+v %v", got, err)
	}
}

// No rows says nothing; yen is not dollars; a truncated sum or an unknown
// answer is an error, never a smaller figure.
func TestAiAndEdgeAnswers(t *testing.T) {
	empty := aiandServer(t, "k", `{"data":[],"has_more":false}`, "", 0)
	if got, err := (AiAnd{BaseURL: empty.URL}).Read(context.Background(), "k"); err != nil || !got.Empty() {
		t.Errorf("no rows: %+v %v", got, err)
	}
	for name, body := range map[string]string{
		"yen":       `{"data":[{"cost":"2.5","currency":"jpy"}],"has_more":false}`,
		"truncated": `{"data":[{"cost":"2.5","currency":"usd"}],"has_more":true,"next_after":null,"next_after_id":null}`,
		"unknown":   `{"items":[]}`,
	} {
		srv := aiandServer(t, "k", body, "", 0)
		if got, err := (AiAnd{BaseURL: srv.URL}).Read(context.Background(), "k"); err == nil {
			t.Errorf("%s: read %+v", name, got)
		}
	}
}

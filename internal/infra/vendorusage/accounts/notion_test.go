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

// notionSpaces is getSpaces for user u1 with a Plus and a Business
// workspace, records wrapped as Notion wraps them.
const notionSpaces = `{"u1":{
  "notion_user":{"u1":{"value":{"value":{"id":"u1","email":"a@b.c"}}}},
  "space":{
    "s-a":{"value":{"value":{"id":"s-a","name":"Personal","subscription_tier":"plus"}}},
    "s-b":{"value":{"value":{"id":"s-b","name":"Team","subscription_tier":"business"}}}
  }}}`

// notionServer serves getSpaces and the allowance for the token "tv2",
// recording the workspace asked about.
func notionServer(t *testing.T, status string, asked *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || cookieValue(r.Header.Get("Cookie"), "token_v2") != "tv2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v3/getSpaces":
			_, _ = w.Write([]byte(notionSpaces))
		case "/api/v3/getCreditRateLimitStatus":
			var body struct {
				SpaceID string `json:"spaceId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			*asked = body.SpaceID
			_, _ = w.Write([]byte(status))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNotionReadsTheAllowance(t *testing.T) {
	var asked string
	srv := notionServer(t, fixture(t, "notion"), &asked)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, cred := range []string{"token_v2=tv2; notion_user_id=u1", "tv2"} {
		got, err := Notion{BaseURL: srv.URL, Now: func() time.Time { return now }}.Read(context.Background(), cred)
		if err != nil || !got.Subscription || len(got.Windows) != 2 || asked != "s-b" {
			t.Fatalf("%q: %+v, %v (asked %q)", cred, got, err, asked)
		}
		if w := got.Windows[0]; w.Name != "6h" || w.Duration != 6*time.Hour || !approx(w.UsedPct, 25) || !w.ResetsAt.Equal(now.Add(time.Hour)) {
			t.Errorf("rolling %+v", w)
		}
		if w := got.Windows[1]; w.Name != "month" || !approx(w.UsedPct, 45) || !w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("month %+v", w)
		}
	}
}

func TestNotionRefusals(t *testing.T) {
	var asked string
	srv := notionServer(t, fixture(t, "notion"), &asked)
	for _, cred := range []string{"token_v2=expired", "notion_user_id=u1"} {
		if _, err := (Notion{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v, want ErrAuth", cred, err)
		}
	}
}

// A workspace without the allowance and an unknown answer are errors,
// never zero use.
func TestNotionShapes(t *testing.T) {
	var asked string
	for _, body := range []string{`{"status":"not_applicable"}`, `{"status":"ok"}`} {
		srv := notionServer(t, body, &asked)
		if got, err := (Notion{BaseURL: srv.URL}).Read(context.Background(), "tv2"); err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s = %+v, %v", body, got, err)
		}
	}
}

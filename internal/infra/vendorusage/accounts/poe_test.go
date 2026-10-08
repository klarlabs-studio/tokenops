package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var poeNow = func() time.Time { return time.Date(2026, 10, 8, 13, 0, 0, 0, time.UTC) }

// poeServer answers the balance and the history pages for "poe-key": the
// first page without a cursor, each next one for the cursor naming it.
func poeServer(t *testing.T, balance string, pages []string) (*httptest.Server, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer poe-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/usage/current_balance":
			_, _ = w.Write([]byte(balance))
		case "/usage/points_history":
			asked = append(asked, r.URL.RawQuery)
			i := 0
			if c := r.URL.Query().Get("starting_after"); c != "" {
				i = -1
				for n, p := range pages {
					if strings.Contains(p, `"query_id": "`+c+`"`) || strings.Contains(p, `"query_id":"`+c+`"`) {
						i = n + 1
					}
				}
			}
			if i < 0 || i >= len(pages) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(pages[i]))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func poeFixture(t *testing.T) (string, []string) {
	t.Helper()
	var f struct {
		Balance json.RawMessage   `json:"current_balance"`
		History []json.RawMessage `json:"points_history"`
	}
	if err := json.Unmarshal([]byte(fixture(t, "poe")), &f); err != nil {
		t.Fatal(err)
	}
	pages := make([]string, len(f.History))
	for i, p := range f.History {
		pages[i] = string(p)
	}
	return string(f.Balance), pages
}

// Poe's points stay points: no dollar balance or spend is made up from
// them. The history is followed by its cursor until it is older than 30
// days, and only the last 30 days' points are summed.
func TestPoeReadsTheBalanceAndThirtyDaysOfPoints(t *testing.T) {
	balance, pages := poeFixture(t)
	srv, asked := poeServer(t, balance, pages)
	got, err := Poe{BaseURL: srv.URL, Now: poeNow}.Read(context.Background(), "poe-key")
	if err != nil || !got.HasCredits || got.Credits != 295932027 || got.CreditsUnit != "points" || got.HasBalance || got.Empty() {
		t.Fatalf("%+v %v", got, err)
	}
	if !got.HasCreditsUsed || !approx(got.CreditsUsed, 2300.5) || got.UsedPeriod != 30*24*time.Hour || got.HasUsed {
		t.Errorf("points spent %+v", got)
	}
	if len(*asked) != 2 || (*asked)[0] != "limit=100" || (*asked)[1] != "limit=100&starting_after=q-3" {
		t.Errorf("history pages asked %v", *asked)
	}
	a := usage.NewEnvelope(poeNow(), Poe{}, got).Attributes
	if a["used_credits"] != "2300.5" || a["used_credits_unit"] != "points" || a["used_credits_period_min"] != "43200" || a["balance_credits"] != "295932027" {
		t.Errorf("attributes %v", a)
	}
	if _, ok := a["extra_usage_used"]; ok {
		t.Error("points were stored as dollars")
	}
}

// The history is an extra: when it fails or cannot be read whole, the
// balance is still read and no spend is claimed.
func TestPoeHistoryIsBestEffort(t *testing.T) {
	balance, _ := poeFixture(t)
	// Every page names the next and none is older than 30 days.
	endless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/usage/current_balance" {
			_, _ = w.Write([]byte(balance))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"query_id":"q","creation_time":1791460800,"cost_points":5}],"next_cursor":"q"}`))
	}))
	t.Cleanup(endless.Close)
	unavailable, _ := poeServer(t, balance, nil)
	noCursor, _ := poeServer(t, balance, []string{`{"data":[{"creation_time":1791460800,"cost_points":5}],"has_more":true}`})
	for name, srv := range map[string]*httptest.Server{"unavailable": unavailable, "no cursor to the rest": noCursor, "too many pages": endless} {
		got, err := Poe{BaseURL: srv.URL, Now: poeNow}.Read(context.Background(), "poe-key")
		if err != nil || !got.HasCredits || got.HasCreditsUsed {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	// A history with nothing in it is whole: no points spent. Rows dated
	// in seconds, milliseconds or ISO are read; an undatable one is skipped.
	srv, _ := poeServer(t, balance, []string{`{"items":[` +
		`{"timestamp":1791460800,"points":1},{"created_at":"2026-10-08T00:00:00Z","point_cost":"2"},` +
		`{"creation_time":1791460800000,"cost_points":4},{"creation_time":"soon","cost_points":100}],"next_cursor":null}`})
	got, err := Poe{BaseURL: srv.URL, Now: poeNow}.Read(context.Background(), "poe-key")
	if err != nil || !got.HasCreditsUsed || got.CreditsUsed != 7 {
		t.Errorf("mixed rows = %+v %v", got, err)
	}
}

func TestPoeRefusedKey(t *testing.T) {
	balance, pages := poeFixture(t)
	srv, _ := poeServer(t, balance, pages)
	if _, err := (Poe{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

// An answer without the balance is an error, never zero points.
func TestPoeUnknownShape(t *testing.T) {
	srv, _ := poeServer(t, `{"balance":12}`, nil)
	if got, err := (Poe{BaseURL: srv.URL}).Read(context.Background(), "poe-key"); err == nil {
		t.Errorf("read %+v", got)
	}
}

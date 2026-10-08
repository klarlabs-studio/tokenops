package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const xaiTeam = "65c1e471-205f-4566-9c5a-07198bcdf4ce"

// xaiFixture is the documented balance answer and CodexBar's usage
// history fixture (Tests/CodexBarTests/XAIProviderTests.swift).
func xaiFixture(t *testing.T) (balance, history string) {
	t.Helper()
	var f struct {
		Balance json.RawMessage `json:"balance"`
		Usage   json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(fixture(t, "xai")), &f); err != nil {
		t.Fatal(err)
	}
	return string(f.Balance), string(f.Usage)
}

// xaiServer answers the team's balance and usage for the management key
// "xai-mgmt", and records the usage request's body.
func xaiServer(t *testing.T, team, balance, history string, usageStatus int) (*httptest.Server, *[]byte) {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer xai-mgmt" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/billing/teams/"+team+"/prepaid/balance":
			_, _ = w.Write([]byte(balance))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/billing/teams/"+team+"/usage":
			body, _ = io.ReadAll(r.Body)
			if usageStatus != http.StatusOK {
				w.WriteHeader(usageStatus)
				return
			}
			_, _ = w.Write([]byte(history))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

var xaiNow = func() time.Time { return time.Date(2027, 1, 15, 9, 30, 0, 0, time.UTC) }

// The ledger is inverted cents: "-1000" is $10 of credit left. The spend
// is the last 30 UTC days' daily sums, asked for as CodexBar asks. Only
// the management key is sent, as a bearer; the team is in the path.
func TestXAIReadsBalanceAndThirtyDaySpend(t *testing.T) {
	balance, history := xaiFixture(t)
	srv, body := xaiServer(t, xaiTeam, balance, history, http.StatusOK)
	got, err := XAI{BaseURL: srv.URL, Now: xaiNow}.Read(context.Background(), xaiTeam+":xai-mgmt")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 10) || got.Scope != "team" {
		t.Fatalf("%+v %v", got, err)
	}
	if !got.HasUsed || !approx(got.UsedUSD, 1.75973725) || got.UsedPeriod != 30*24*time.Hour || got.Subscription {
		t.Errorf("spend %+v", got)
	}
	var req struct {
		AnalyticsRequest struct {
			TimeRange struct {
				StartTime, EndTime, Timezone string
			} `json:"timeRange"`
			TimeUnit string `json:"timeUnit"`
			Values   []struct {
				Name, Aggregation string
			} `json:"values"`
		} `json:"analyticsRequest"`
	}
	if err := json.Unmarshal(*body, &req); err != nil {
		t.Fatal(err)
	}
	a := req.AnalyticsRequest
	if a.TimeRange.StartTime != "2026-12-17 00:00:00" || a.TimeRange.EndTime != "2027-01-15 09:30:00" ||
		a.TimeRange.Timezone != "Etc/GMT" || a.TimeUnit != "TIME_UNIT_DAY" ||
		len(a.Values) != 1 || a.Values[0].Name != "usd" || a.Values[0].Aggregation != "AGGREGATION_SUM" {
		t.Errorf("usage request %s", *body)
	}
	env := usage.NewEnvelope(xaiNow(), XAI{}, got).Attributes
	if env["extra_usage_used"] != "1.76" || env["extra_usage_period_min"] != "43200" || env["balance_usd"] != "10.00" {
		t.Errorf("attributes %v", env)
	}
}

// The history is an extra: when it is unavailable, malformed or cut
// short, the balance is still read and no spend is claimed. A refused key
// fails the reading.
func TestXAISpendIsBestEffort(t *testing.T) {
	balance, history := xaiFixture(t)
	cases := map[string]struct {
		history string
		status  int
	}{
		"unavailable":     {history, http.StatusInternalServerError},
		"no series":       {`{}`, http.StatusOK},
		"null series":     {`{"timeSeries":null,"limitReached":false}`, http.StatusOK},
		"no data points":  {`{"timeSeries":[{}],"limitReached":false}`, http.StatusOK},
		"no values":       {`{"timeSeries":[{"dataPoints":[{"timestamp":"2027-01-15T00:00:00Z"}]}],"limitReached":false}`, http.StatusOK},
		"negative":        {`{"timeSeries":[{"dataPoints":[{"timestamp":"2027-01-15T00:00:00Z","values":[-1]}]}]}`, http.StatusOK},
		"cut short":       {`{"timeSeries":[{"dataPoints":[{"timestamp":"2027-01-15T00:00:00Z","values":[3]}]}],"limitReached":true}`, http.StatusOK},
		"bad timestamp":   {`{"timeSeries":[{"dataPoints":[{"timestamp":"yesterday","values":[3]}]}]}`, http.StatusOK},
		"refused history": {history, http.StatusForbidden},
	}
	for name, c := range cases {
		srv, _ := xaiServer(t, xaiTeam, balance, c.history, c.status)
		got, err := XAI{BaseURL: srv.URL, Now: xaiNow}.Read(context.Background(), xaiTeam+":xai-mgmt")
		if name == "refused history" {
			if !errors.Is(err, usage.ErrAuth) {
				t.Errorf("%s: err %v, want ErrAuth", name, err)
			}
			continue
		}
		if err != nil || !got.HasBalance || got.HasUsed {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	// An empty history is a whole one: nothing was spent.
	srv, _ := xaiServer(t, xaiTeam, balance, `{"timeSeries":[],"limitReached":false}`, http.StatusOK)
	got, err := XAI{BaseURL: srv.URL, Now: xaiNow}.Read(context.Background(), xaiTeam+":xai-mgmt")
	if err != nil || !got.HasUsed || got.UsedUSD != 0 {
		t.Errorf("empty history = %+v %v", got, err)
	}
}

// A refused key, a team the key does not belong to, and a credential that
// is not TEAM_ID:MANAGEMENT_KEY (an inference key alone) are all refused.
func TestXAIRefusals(t *testing.T) {
	balance, history := xaiFixture(t)
	srv, _ := xaiServer(t, xaiTeam, balance, history, http.StatusOK)
	for _, cred := range []string{xaiTeam + ":bad", "other-team:xai-mgmt", "xai-inference-key", ":xai-mgmt", "../x:xai-mgmt"} {
		if _, err := (XAI{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: err %v", cred, err)
		}
	}
}

func TestXAIUnknownShape(t *testing.T) {
	for _, body := range []string{`{}`, `{"total":{"val":"ten"}}`} {
		srv, _ := xaiServer(t, "t", body, `{"timeSeries":[]}`, http.StatusOK)
		got, err := XAI{BaseURL: srv.URL}.Read(context.Background(), "t:xai-mgmt")
		if err == nil {
			t.Errorf("%s: read %+v", body, got)
		}
	}
}

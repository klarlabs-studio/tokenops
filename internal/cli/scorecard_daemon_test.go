package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard"
)

// scorecardDaemon answers /api/scorecard with s and records the requests.
func scorecardDaemon(t *testing.T, s *spending.ScorecardReport) (listen string, asked func() string) {
	t.Helper()
	var (
		mu  sync.Mutex
		got []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.URL.RequestURI())
		mu.Unlock()
		if r.URL.Path != "/api/scorecard" {
			http.NotFound(w, r)
			return
		}
		b, err := json.Marshal(s)
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host, func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(got, " ")
	}
}

func daemonScorecard() *spending.ScorecardReport {
	s := scorecard.NewWithAgentKPIs(42, 30, 91, scorecard.AgentKPIInputs{
		ConfirmationGateRatePct: 25, ConfirmationGateComputed: true}, "")
	s.GeneratedAt = time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	s.PlanUseDays = 14
	return s
}

// scorecard prints the scorecard the daemon computed, for the period
// asked, as the daemon would have it printed.
func TestScorecardPrintsTheDaemonsScorecard(t *testing.T) {
	s := daemonScorecard()
	listen, asked := scorecardDaemon(t, s)
	out, err := executeRoot(t, "--listen", listen, "scorecard", "--days", "14", "--baseline-ref", "after-upgrade")
	if err != nil {
		t.Fatal(err)
	}
	want := *s
	want.BaselineRef = "after-upgrade"
	if out != want.String() {
		t.Errorf("printed\n%s\nwant the daemon's scorecard\n%s", out, want.String())
	}
	if got := asked(); got != "/api/scorecard?since_days=14" {
		t.Errorf("asked the daemon for %q", got)
	}
	jsonOut, err := executeRoot(t, "--listen", listen, "scorecard", "--json")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.MarshalJSON()
	if strings.TrimSpace(jsonOut) != string(b) {
		t.Errorf("--json printed\n%s\nwant the daemon's\n%s", jsonOut, b)
	}
}

// An override, --db, --local or no daemon answering: the scorecard is
// computed here, as before.
func TestScorecardComputesHereWhenItIsNotTheDaemons(t *testing.T) {
	listen, asked := scorecardDaemon(t, daemonScorecard())
	missing := filepath.Join(t.TempDir(), "events.db")
	for _, args := range [][]string{
		{"--listen", listen, "scorecard", "--local", "--db", missing},
		{"--listen", listen, "scorecard", "--local"},
		{"--listen", listen, "scorecard", "--db", missing},
		{"--listen", listen, "scorecard", "--fvt-seconds", "30"},
		{"--listen", listen, "scorecard", "--teu-pct", "30"},
		{"--listen", listen, "scorecard", "--sac-pct", "30"},
	} {
		if _, err := executeRoot(t, args...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	if got := asked(); got != "" {
		t.Errorf("asked the daemon for %q", got)
	}
	// Nothing listening: computed here, from the configured store.
	out, err := executeRoot(t, "--listen", "127.0.0.1:1", "scorecard")
	if err != nil || !strings.Contains(out, "Scorecard") {
		t.Errorf("with no daemon: %v\n%s", err, out)
	}
}

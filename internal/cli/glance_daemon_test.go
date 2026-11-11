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

	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// fakeDaemon answers the glance routes with p and r, and records what
// was asked.
type fakeDaemon struct {
	mu    sync.Mutex
	asked []string
	srv   *httptest.Server
}

func newFakeDaemon(t *testing.T, p *headroom.GlancePayload, r findings.Report) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		d.mu.Lock()
		d.asked = append(d.asked, req.URL.RequestURI())
		d.mu.Unlock()
		var body any
		switch req.URL.Path {
		case "/api/glance":
			body = p
		case "/api/findings":
			body = r
		default:
			http.NotFound(w, req)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *fakeDaemon) listen(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(d.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func (d *fakeDaemon) requests() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.asked, " ")
}

func daemonGlance(computedAt time.Time, costs map[string]spending.ProviderCost) *headroom.GlancePayload {
	var g headroom.Glance
	g.Headroom.Reports = []plans.HeadroomReport{{Provider: "anthropic", Display: "Claude Ultra",
		Windows: []plans.VendorWindow{{Name: "5h", UsedPct: 41, ResetsIn: "2h0m0s"}}}}
	p := g.Payload()
	p.ComputedAt, p.ProviderCosts = computedAt, costs
	return p
}

// glance draws what the daemon holds: no store is opened (there is none
// here), the time at the top is when the daemon computed it, and the
// costs are the daemon's.
func TestGlanceDrawsTheDaemonsAnswer(t *testing.T) {
	at := time.Date(2026, 11, 11, 9, 41, 0, 0, time.Local)
	costs := map[string]spending.ProviderCost{"anthropic": {
		Today: spending.Usage{Tokens: 5e6, APIEquivalentUSD: 12}, Last30: spending.Usage{Tokens: 9e8, APIEquivalentUSD: 777}}}
	r := findings.Report{Findings: []findings.Finding{{Key: "coach:x", Title: "A finding from the daemon"}}}
	d := newFakeDaemon(t, daemonGlance(at, costs), r)

	out, err := executeRoot(t, "--listen", d.listen(t), "glance", "--color", "never")
	if err != nil {
		t.Fatalf("glance: %v\n%s", err, out)
	}
	for _, want := range []string{"PLAN Ultra", "59% left", "09:41", "$777", "A finding from the daemon"} {
		if !strings.Contains(out, want) {
			t.Errorf("glance lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not read in time") {
		t.Errorf("costs the daemon carried are said not read:\n%s", out)
	}
	if got := d.requests(); got != "/api/glance /api/findings?dismissed=include" {
		t.Errorf("asked the daemon for %q", got)
	}
}

// --json is the daemon's answer as it gave it, the costs included.
func TestGlanceJSONIsTheDaemonsPayload(t *testing.T) {
	costs := map[string]spending.ProviderCost{"anthropic": {Today: spending.Usage{Tokens: 7}}}
	d := newFakeDaemon(t, daemonGlance(time.Now(), costs), findings.Report{})
	out, err := executeRoot(t, "--listen", d.listen(t), "glance", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var p headroom.GlancePayload
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("not a glance payload: %v\n%s", err, out)
	}
	if p.ProviderCosts["anthropic"].Today.Tokens != 7 || p.ComputedAt.IsZero() {
		t.Errorf("payload = %+v, want the daemon's", p)
	}
}

// A daemon from before the glance carried costs: the cards say the costs
// were not read, rather than read 30 days of usage here.
func TestGlanceFromAnOlderDaemonSaysTheCostsWereNotRead(t *testing.T) {
	d := newFakeDaemon(t, daemonGlance(time.Now(), nil), findings.Report{})
	out, err := executeRoot(t, "--listen", d.listen(t), "glance", "--color", "never")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not read in time") {
		t.Errorf("an older daemon's glance does not say its costs were not read:\n%s", out)
	}
}

// With --local or --db, or with no daemon answering, glance computes the
// glance here: it is not the daemon's to answer then.
func TestGlanceComputesHereWithoutTheDaemon(t *testing.T) {
	d := newFakeDaemon(t, daemonGlance(time.Now(), nil), findings.Report{})
	missing := filepath.Join(t.TempDir(), "events.db")
	for _, args := range [][]string{
		{"--listen", d.listen(t), "glance", "--local", "--db", missing},
		{"--listen", d.listen(t), "glance", "--db", missing},
		{"--listen", "127.0.0.1:1", "glance", "--db", missing},
	} {
		_, err := executeRoot(t, args...)
		// Computed here, the glance reads the store at --db, which is not
		// there.
		if err == nil || !strings.Contains(err.Error(), "open store") {
			t.Errorf("%v: err = %v, want the local computation's", args, err)
		}
	}
	// --local alone reads the configured store, which another test may
	// have made: what matters is that the daemon is not asked.
	_, _ = executeRoot(t, "--listen", d.listen(t), "glance", "--local")
	if got := d.requests(); got != "" {
		t.Errorf("asked the daemon for %q", got)
	}
	// No daemon at the configured address and no --db: computed here.
	_, err := executeRoot(t, "--listen", "127.0.0.1:1", "glance")
	if err != nil && strings.Contains(err.Error(), "daemon") {
		t.Errorf("no daemon answering made glance fail: %v", err)
	}
}

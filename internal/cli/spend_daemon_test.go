package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// reportDaemon answers path with body and records what was asked.
func reportDaemon(t *testing.T, path string, body any) (listen string, asked func() []*url.URL) {
	t.Helper()
	var (
		mu  sync.Mutex
		got []*url.URL
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.URL)
		mu.Unlock()
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host, func() []*url.URL {
		mu.Lock()
		defer mu.Unlock()
		return append([]*url.URL(nil), got...)
	}
}

// roundTrip is v as the daemon sends it and the CLI reads it back.
func roundTrip[T any](t *testing.T, v T) T {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The reports read back from the daemon's JSON print as the reports
// computed here: the CLI prints what the daemon computed.
func TestSpendReportsReadTheSameFromTheWire(t *testing.T) {
	var a, b bytes.Buffer
	units := unitsFixture()
	writeUnitReport(&a, units)
	writeUnitReport(&b, roundTrip(t, units))
	if a.String() != b.String() {
		t.Errorf("units read back from the wire\n%s\nwant\n%s", b.String(), a.String())
	}
	a.Reset()
	b.Reset()
	waste := wasteFixture()
	writeWasteReport(&a, waste)
	writeWasteReport(&b, roundTrip(t, waste))
	if a.String() != b.String() {
		t.Errorf("waste read back from the wire\n%s\nwant\n%s", b.String(), a.String())
	}
}

// spend units and spend waste print the daemon's report for the period
// asked; with --local or --db, or no daemon answering, they compute it.
func TestSpendReportsAskTheDaemon(t *testing.T) {
	for _, c := range []struct {
		cmd, path string
		body      any
		print     func(*bytes.Buffer)
	}{
		{"units", "/api/spend/units", unitsFixture(), func(b *bytes.Buffer) { writeUnitReport(b, unitsFixture()) }},
		{"waste", "/api/spend/waste", wasteFixture(), func(b *bytes.Buffer) { writeWasteReport(b, wasteFixture()) }},
	} {
		t.Run(c.cmd, func(t *testing.T) {
			listen, asked := reportDaemon(t, c.path, c.body)
			before := time.Now()
			out, err := executeRoot(t, "--listen", listen, "spend", c.cmd, "--since", "72h")
			if err != nil {
				t.Fatal(err)
			}
			var want bytes.Buffer
			c.print(&want)
			if out != want.String() {
				t.Errorf("printed\n%s\nwant the daemon's report\n%s", out, want.String())
			}
			got := asked()
			if len(got) != 1 || got[0].Path != c.path {
				t.Fatalf("asked the daemon for %v", got)
			}
			since, err := time.Parse(time.RFC3339, got[0].Query().Get("since"))
			if err != nil || since.Before(before.Add(-72*time.Hour-time.Second)) || since.After(time.Now().Add(-72*time.Hour)) {
				t.Errorf("asked since %q (%v), want 72 hours back", got[0].Query().Get("since"), err)
			}

			jsonOut, err := executeRoot(t, "--listen", listen, "spend", c.cmd, "--json")
			if err != nil || !strings.Contains(jsonOut, `"since"`) {
				t.Errorf("--json: %v\n%s", err, jsonOut)
			}

			missing := filepath.Join(t.TempDir(), "events.db")
			n := len(asked())
			for _, args := range [][]string{
				{"--listen", listen, "spend", c.cmd, "--local"},
				{"--listen", listen, "spend", c.cmd, "--db", missing},
			} {
				_, _ = executeRoot(t, args...)
			}
			if len(asked()) != n {
				t.Errorf("--local or --db asked the daemon: %v", asked()[n:])
			}
			// Nothing listening: computed here, from the configured store.
			if _, err := executeRoot(t, "--listen", "127.0.0.1:1", "spend", c.cmd); err != nil && !strings.Contains(err.Error(), "event store") {
				t.Errorf("with no daemon: %v", err)
			}
		})
	}
}

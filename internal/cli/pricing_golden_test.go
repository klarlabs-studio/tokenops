package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
)

// executeRootStreams runs the root command and returns stdout and stderr
// together, so a golden pins the warnings as well as the report.
func executeRootStreams(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRoot()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	return outBuf.String() + "--- stderr ---\n" + errBuf.String(), err
}

// pricingFixture writes two snapshots: an older one and a newer one that
// moves a rate, adds a model, drops one, and carries an anomaly (a cache
// read dearer than input).
func pricingFixture(t *testing.T) (dir string, older, newer time.Time) {
	t.Helper()
	dir = t.TempDir()
	older = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	newer = time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	snaps := []pricing.Snapshot{
		{Source: "litellm", SourceURL: "https://example.test/rates.json", FetchedAt: older, Rates: map[string]pricing.Rate{
			"anthropic/claude-opus-4-7": {InputPerMillion: 5, OutputPerMillion: 25, CachedInputPerMillion: 0.5},
			"openai/gpt-4o":             {InputPerMillion: 2.5, OutputPerMillion: 10, CachedInputPerMillion: 1.25},
			"mistral/mistral-large":     {InputPerMillion: 2, OutputPerMillion: 6},
		}},
		{Source: "litellm", SourceURL: "https://example.test/rates.json", FetchedAt: newer, Rates: map[string]pricing.Rate{
			"anthropic/claude-opus-4-7": {InputPerMillion: 5, OutputPerMillion: 25, CachedInputPerMillion: 1.5},
			"openai/gpt-4o":             {InputPerMillion: 2.5, OutputPerMillion: 10, CachedInputPerMillion: 3},
			"openai/gpt-4o-mini":        {InputPerMillion: 0.15, OutputPerMillion: 0.6},
		}},
	}
	for _, s := range snaps {
		if _, err := pricing.SaveSnapshot(dir, s); err != nil {
			t.Fatal(err)
		}
	}
	return dir, older, newer
}

// fetchedAt is the refresh's own clock, which no fixture can pin.
var fetchedAt = regexp.MustCompile(`\(as of [^)]+\)`)

// TestPricingOutputCharacterization pins the `tokenops pricing` tree
// byte for byte, stderr included, so moving it onto a capability cannot
// change it.
func TestPricingOutputCharacterization(t *testing.T) {
	dir, older, newer := pricingFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"claude-opus-4-7":{"litellm_provider":"anthropic","input_cost_per_token":5e-6,"output_cost_per_token":2.5e-5,"cache_read_input_token_cost":5e-7},` +
			`"gpt-4o":{"litellm_provider":"openai","input_cost_per_token":2.5e-6,"output_cost_per_token":1e-5,"cache_read_input_token_cost":5e-6}}`))
	}))
	defer srv.Close()
	from, to := older.Format(time.RFC3339), newer.Format(time.RFC3339)
	cases := []struct {
		golden  string
		args    []string
		wantErr bool
	}{
		{golden: "pricing/show.txt", args: []string{"pricing", "show", "--dir", dir}},
		{golden: "pricing/show_older.json", args: []string{"pricing", "show", "--dir", dir, "--snapshot", from, "--json"}},
		{golden: "pricing/diff.txt", args: []string{"pricing", "diff", "--dir", dir, "--from", from, "--to", to}},
		{golden: "pricing/diff.json", args: []string{"pricing", "diff", "--dir", dir, "--from", from, "--to", to, "--json"}},
		{golden: "pricing/diff_none.txt", args: []string{"pricing", "diff", "--dir", dir, "--from", to, "--to", to}},
		{golden: "pricing/lint_newer.txt", args: []string{"pricing", "lint", "--dir", dir}, wantErr: true},
		{golden: "pricing/lint_newer.json", args: []string{"pricing", "lint", "--dir", dir, "--json"}, wantErr: true},
		{golden: "pricing/lint_older.txt", args: []string{"pricing", "lint", "--dir", dir, "--snapshot", from}},
		{golden: "pricing/refresh_dry.txt", args: []string{"pricing", "refresh", "--dir", dir, "--source", "litellm", "--url", srv.URL, "--dry-run"}},
		{golden: "pricing/show_missing.txt", args: []string{"pricing", "show", "--dir", dir, "--snapshot", "2020-01-01T00:00:00Z"}, wantErr: true},
		{golden: "pricing/unknown_source.txt", args: []string{"pricing", "refresh", "--dir", dir, "--source", "nope"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			out, err := executeRootStreams(t, tc.args...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("%v: err %v, want error %v", tc.args, err, tc.wantErr)
			}
			if err != nil {
				out += "--- error ---\n" + err.Error() + "\n"
			}
			out = strings.ReplaceAll(out, srv.URL, "<SOURCE>")
			out = strings.ReplaceAll(out, dir, "<DIR>")
			out = fetchedAt.ReplaceAllString(out, "(as of <NOW>)")
			assertGolden(t, tc.golden, out)
		})
	}

	// A real refresh writes the snapshot it fetched; the next show reads it.
	out, err := executeRootStreams(t, "pricing", "refresh", "--dir", dir, "--source", "litellm", "--url", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Snapshot written: "+filepath.Join(dir, "snapshots")) {
		t.Errorf("refresh did not report the snapshot it wrote:\n%s", out)
	}
	if latest, ok := pricing.LatestSnapshot(dir); !ok || !latest.FetchedAt.After(newer) {
		t.Errorf("latest snapshot %v, want the one refresh just wrote", latest.FetchedAt)
	}
}

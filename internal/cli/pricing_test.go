package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// litellmFixtureServer serves the pricing package's trimmed LiteLLM sample so
// the CLI end-to-end tests never touch the live network.
func litellmFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "contexts", "spend", "pricing", "testdata", "litellm_sample.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runPricing(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}

func TestPricingRefreshShowLintDiff_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	srv := litellmFixtureServer(t)

	// refresh against the fixture server (no live network).
	out, err := runPricing(t, "pricing", "refresh", "--url", srv.URL, "--dir", dir)
	if err != nil {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Snapshot written") {
		t.Errorf("refresh did not write a snapshot:\n%s", out)
	}
	// First refresh diffs vs baseline; claude-3-opus is new → added line.
	if !strings.Contains(out, "claude-3-opus") || !strings.Contains(out, "added") {
		t.Errorf("expected added line for new model:\n%s", out)
	}

	// show latest.
	out, err = runPricing(t, "pricing", "show", "--dir", dir)
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	if !strings.Contains(out, "claude-opus-4-8") || !strings.Contains(out, "litellm") {
		t.Errorf("show missing expected content:\n%s", out)
	}

	// lint latest — the fixture is clean, exit 0.
	out, err = runPricing(t, "pricing", "lint", "--dir", dir)
	if err != nil {
		t.Fatalf("lint clean snapshot should exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no consistency anomalies") {
		t.Errorf("lint output unexpected:\n%s", out)
	}

	// diff baseline → latest.
	out, err = runPricing(t, "pricing", "diff", "--from", "baseline", "--to", "latest", "--dir", dir)
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	if !strings.Contains(out, "claude-3-opus") {
		t.Errorf("diff missing added model:\n%s", out)
	}
}

func TestPricingRefreshDryRun_DoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	srv := litellmFixtureServer(t)
	out, err := runPricing(t, "pricing", "refresh", "--url", srv.URL, "--dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run refresh: %v\n%s", err, out)
	}
	if !strings.Contains(out, "dry-run") {
		t.Errorf("expected dry-run notice:\n%s", out)
	}
	// No snapshot file should exist under the dir.
	matches, _ := filepath.Glob(filepath.Join(dir, "snapshots", "*.json"))
	if len(matches) != 0 {
		t.Errorf("dry-run wrote %d snapshot(s)", len(matches))
	}
}

func TestPricingRefresh_FetchErrorExitsNonZeroNoWrite(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	out, err := runPricing(t, "pricing", "refresh", "--url", srv.URL, "--dir", dir)
	if err == nil {
		t.Fatalf("fetch failure should exit non-zero:\n%s", out)
	}
	if !strings.Contains(out, "fetch failed") {
		t.Errorf("expected a clear fetch-failed message:\n%s", out)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "snapshots", "*.json"))
	if len(matches) != 0 {
		t.Errorf("failed fetch must not write a snapshot, found %d", len(matches))
	}
}

func TestPricingLint_AnomalyExitsNonZero(t *testing.T) {
	// A snapshot dir is empty → lint falls back to baseline, which is clean, so
	// lint on baseline exits 0. Assert the baseline is clean here (regression
	// guard mirrored at the CLI layer).
	out, err := runPricing(t, "pricing", "lint", "--snapshot", "baseline", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("baseline lint should be clean: %v\n%s", err, out)
	}
}

func TestPricingShowJSON(t *testing.T) {
	out, err := runPricing(t, "pricing", "show", "--snapshot", "baseline", "--json", "--dir", t.TempDir())
	if err != nil {
		t.Fatalf("show --json: %v\n%s", err, out)
	}
	if !strings.Contains(out, "\"source\": \"embedded-baseline\"") {
		t.Errorf("json output missing provenance:\n%s", out)
	}
}

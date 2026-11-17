package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/workquote"
	"go.klarlabs.de/tokenops/internal/contexts/work/quote"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// quoteHome is a home with a stored quote basis of n synthetic edits on
// model, and an empty event store; it returns the store's path.
func quoteHome(t *testing.T, model string, n int) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	now := time.Now().UTC()
	b := workquote.Basis{Version: workquote.Version, ComputedAt: now}
	for i := range n {
		b.Samples = append(b.Samples, quote.Sample{Client: "claude-code", Provider: "anthropic", Model: model, Kind: "edit",
			At: now.Add(-time.Duration(i+1) * time.Hour), USD: float64(i + 1), Tokens: int64(i+1) * 1000})
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".tokenops", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work-quote-basis.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(home, "events.db")
	st, err := sqlite.Open(context.Background(), db, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	return db
}

func TestTaskQuotePricesFromTheStoredHistory(t *testing.T) {
	db := quoteHome(t, "claude-opus-4-5", 20)
	out := runRoot(t, "task", "quote", "fix the flaky retry test", "--model", "claude-opus-4-5", "--db", db)
	for _, want := range []string{"Quote (an estimate)", "$10.50 typical · $18.10 p90", "Calibration:", "same model and kind of work"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "flaky retry") {
		t.Error("the instruction was echoed")
	}
}

func TestTaskQuoteWithTooLittleHistoryGivesNoNumber(t *testing.T) {
	db := quoteHome(t, "claude-opus-4-5", 4)
	out := runRoot(t, "task", "quote", "fix it", "--model", "claude-opus-4-5", "--db", db, "--json")
	var r workquote.Result
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.Quoted() || r.P90USD != 0 || r.Samples != 4 {
		t.Fatalf("result = %+v", r)
	}
}

func TestTaskQuoteNeedsAModel(t *testing.T) {
	root := NewRoot()
	var out bytes.Buffer
	root.SetArgs([]string{"task", "quote", "fix it"})
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--model") {
		t.Fatalf("err = %v", err)
	}
}

package planhistory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

func TestAppendAndLoad(t *testing.T) {
	f := File{Path: filepath.Join(t.TempDir(), "h", "plan-history.jsonl")}
	if h, err := f.Load(); err != nil || h != nil {
		t.Fatalf("missing file: %v %v", h, err)
	}
	at := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	if err := f.Append(plans.Binding{Provider: "openai", Plan: "gpt-pro-5x", From: at, Recorded: at}); err != nil {
		t.Fatal(err)
	}
	h, err := f.Load()
	if err != nil || len(h) != 1 || h[0].Plan != "gpt-pro-5x" || !h[0].From.Equal(at) {
		t.Fatalf("Load = %+v, %v", h, err)
	}
	info, _ := os.Stat(f.Path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

package compactlever

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// The coach report calls Check on every GET /api/coach and /api/findings.
// Listing opencode's models scans its message store, so only Apply, which
// uses them, may pay for that.
func TestOnlyApplyListsOpencodeModels(t *testing.T) {
	p := paths(t)
	write(t, p.OpencodeModels, opencodeCatalog)
	write(t, p.OpencodeConfig, opencodeConfig)
	listed := 0
	l := Levers{
		Paths: p,
		Plan:  Plan{ClaudeCompactAt: 600_000, CodexCompactAt: 150_000, OpencodeShare: 0.6},
		Now:   func() time.Time { return now },
		opencodeModels: func() []string {
			listed++
			return []string{"anthropic/claude-opus-4-8"}
		},
	}
	l.Check()
	if listed != 0 {
		t.Fatalf("Check listed opencode models %d times; it never uses them", listed)
	}
	rs, err := l.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if listed != 1 {
		t.Fatalf("Apply listed opencode models %d times, want 1", listed)
	}
	set := false
	for _, r := range rs {
		if r.Client == ClientOpencode && r.Key == "anthropic/claude-opus-4-8" {
			set = true
		}
	}
	if !set {
		t.Fatalf("Apply did not set the listed opencode model: %+v", rs)
	}
}

// New must not list the models itself: that is the scan the coach report
// was paying for.
func TestNewDefersOpencodeModels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
	l, err := New(config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if l.Plan.OpencodeModels != nil {
		t.Fatalf("New resolved opencode models eagerly: %v", l.Plan.OpencodeModels)
	}
	if l.opencodeModels == nil {
		t.Fatal("New left Apply no way to list opencode models")
	}
}

// Pi's models are read from its own session files, as provider/modelId
// in Pi's own naming: the key its settings take.
func TestPiModelsUsedReadsPisSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	dir := filepath.Join(home, "pi")
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	src := filepath.Join("..", "..", "contexts", "spend", "vendorusage", "pisessions", "testdata", "pi", "--corpus--",
		"2026-09-30T12-00-00-000Z_pi-proof-session.jsonl")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(dir, "sessions", "--corpus--")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(sessions, "s.jsonl"), string(b))
	got := piModelsUsed(time.Time{})
	if !slices.Contains(got, "anthropic/claude-sonnet-4-6") {
		t.Errorf("pi models = %v; want anthropic/claude-sonnet-4-6 among them", got)
	}
	if len(piModelsUsed(time.Now().Add(time.Hour))) != 0 {
		t.Error("listed models from sessions older than the cutoff")
	}
}

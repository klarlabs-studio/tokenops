package compactlever

import (
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

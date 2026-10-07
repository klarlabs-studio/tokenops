package checkup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const claudeLine = `{"type":"assistant","timestamp":"%s","sessionId":"s1","message":{"id":"%s","model":"claude-sonnet-5","usage":{"input_tokens":10,"output_tokens":20}}}` + "\n"

func writeClaudeSession(t *testing.T, root, project, name, msgID string, at time.Time) string {
	t.Helper()
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	line := strings.Replace(strings.Replace(claudeLine, "%s", at.UTC().Format(time.RFC3339Nano), 1), "%s", msgID, 1)
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their mode")
	}
}

// A client that is not installed is silent; one whose records exist but
// cannot be read says so, or its usage would read as "none".
func TestReadClaudeAtSurfacesUnreadableRecords(t *testing.T) {
	skipIfRoot(t)
	since := time.Now().Add(-time.Hour)
	cases := []struct {
		name      string
		setup     func(t *testing.T) string
		wantTurns int
		wantWarn  string
	}{
		{
			name:  "missing root is silent",
			setup: func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") },
		},
		{
			name: "readable files are counted",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeClaudeSession(t, root, "p", "a.jsonl", "msg_a", time.Now())
				return root
			},
			wantTurns: 1,
		},
		{
			name: "unreadable root warns",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeClaudeSession(t, root, "p", "a.jsonl", "msg_a", time.Now())
				if err := os.Chmod(root, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
				return root
			},
			wantWarn: "Claude Code",
		},
		{
			name: "unreadable file warns and the rest is counted",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeClaudeSession(t, root, "p", "a.jsonl", "msg_a", time.Now())
				bad := writeClaudeSession(t, root, "p", "b.jsonl", "msg_b", time.Now())
				if err := os.Chmod(bad, 0); err != nil {
					t.Fatal(err)
				}
				return root
			},
			wantTurns: 1,
			wantWarn:  "1 session file(s) unreadable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := tc.setup(t)
			turns := 0
			err := readClaudeAt(context.Background(), root, nil, since, func(turn) { turns++ })
			if turns != tc.wantTurns {
				t.Errorf("turns = %d, want %d", turns, tc.wantTurns)
			}
			switch {
			case tc.wantWarn == "" && err != nil:
				t.Errorf("unexpected warning: %v", err)
			case tc.wantWarn != "" && (err == nil || !strings.Contains(err.Error(), tc.wantWarn)):
				t.Errorf("warning = %v, want it to mention %q", err, tc.wantWarn)
			}
		})
	}
}

// A read cut short by the caller's context reports totals as partial
// instead of passing a fraction off as the whole.
func TestReadUsageMarksCancelledReadPartial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GEMINI_CLI_HOME", home)
	t.Setenv("OPENCODE_DB", filepath.Join(home, "absent.db"))
	writeClaudeSession(t, filepath.Join(home, ".claude", "projects"), "p", "a.jsonl", "msg_a", time.Now())

	cases := []struct {
		name        string
		cancel      bool
		wantPartial bool
	}{
		{"complete read", false, false},
		{"cancelled read", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			got := readUsage(ctx, home, time.Now().Add(-time.Hour))
			if got.partial != tc.wantPartial {
				t.Fatalf("partial = %v, want %v (warnings %v)", got.partial, tc.wantPartial, got.warnings)
			}
			if tc.wantPartial && !strings.Contains(strings.Join(got.warnings, "\n"), "partial") {
				t.Errorf("warnings %v do not say the totals are partial", got.warnings)
			}
			if !tc.wantPartial && len(got.usage) != 1 {
				t.Errorf("usage = %+v, want the one Claude turn", got.usage)
			}
		})
	}
}

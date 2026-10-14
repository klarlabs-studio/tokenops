package coachhook

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Tip is a session's open tip as another surface shows it: the status
// line reads it on every refresh until the tip resolves. It carries the
// tip's kind and, for a quota tip, which window it was about. Never any
// prompt content.
type Tip struct {
	ID   string
	Kind string
	// Window names the quota window ("Claude weekly"), empty for other
	// tips; ResetsAt is when it resets.
	Window   string
	ResetsAt time.Time
	// RunsOutAt is when a run-out warning said the window runs out.
	RunsOutAt time.Time
}

// OpenTip reads the session's open tip from the hook's state in dir
// (default ~/.tokenops/coach-hook). False when there is none.
func OpenTip(dir, sessionID string) (Tip, bool) {
	if sessionID == "" {
		return Tip{}, false
	}
	o := loadSession(resolveDir(dir), sessionID).OpenTip
	if o == nil || o.ID == "" || o.Kind == "" {
		return Tip{}, false
	}
	return Tip{ID: o.ID, Kind: o.Kind, Window: o.Window, ResetsAt: o.ResetsAt, RunsOutAt: o.RunsOutAt}, true
}

// Urgent reports a tip about work about to stop: the session budget
// spent, or a quota window running out or used up. They are the tips that never
// go quiet, and the only ones `quiet` verbosity shows.
func (t Tip) Urgent() bool { return t.Kind != "" && !quietable(t.Kind) }

// Short words the tip for a narrow status line, empty when there is
// nothing current to say: a quota tip whose window has since reset is
// stale.
func (t Tip) Short(now time.Time) string {
	switch {
	case t.Kind == compactKind:
		return "compact now (/compact)"
	case t.Kind == "budget_over":
		return "past the session budget → fresh session"
	case strings.HasPrefix(t.Kind, "budget_"):
		return strings.TrimPrefix(t.Kind, "budget_") + "% of the session budget used"
	case t.Kind == burnKind:
		return t.shortBurn(now)
	case strings.HasPrefix(t.Kind, "quota_"):
		return t.shortQuota(now)
	}
	return ""
}

func (t Tip) shortBurn(now time.Time) string {
	if !t.ResetsAt.IsZero() && !t.ResetsAt.After(now) {
		return ""
	}
	window := t.Window
	if window == "" {
		window = "plan window"
	}
	if !t.RunsOutAt.After(now) {
		return window + " about to run out → smaller model"
	}
	return fmt.Sprintf("%s out in ~%s → smaller model", window, strings.ReplaceAll(formatSpan(t.RunsOutAt.Sub(now)), " ", ""))
}

func (t Tip) shortQuota(now time.Time) string {
	used, err := strconv.Atoi(strings.TrimPrefix(t.Kind, "quota_"))
	if err != nil {
		return ""
	}
	if !t.ResetsAt.IsZero() && !t.ResetsAt.After(now) {
		return ""
	}
	window := t.Window
	if window == "" {
		window = "plan window"
	}
	if used >= 100 {
		if t.ResetsAt.IsZero() {
			return window + " used up → use another plan"
		}
		return fmt.Sprintf("%s out for %s → use another plan", window, strings.ReplaceAll(formatSpan(t.ResetsAt.Sub(now)), " ", ""))
	}
	return fmt.Sprintf("%s %d%% used → /compact, smaller model", window, used)
}

// MarkSeen records that the status line showed the session's tip id and
// reports whether this is the first time, so the caller writes the
// follow-through ledger once per tip rather than on every refresh.
//
// The mark lives in a file of its own: the hook rewrites its state file
// whole on every Stop and would drop it.
func MarkSeen(dir, sessionID, id string) bool {
	if sessionID == "" || id == "" {
		return false
	}
	path := seenFile(resolveDir(dir), sessionID)
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) == id { //nolint:gosec // TokenOps' own state
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false
	}
	return os.WriteFile(path, []byte(id), 0o600) == nil
}

func seenFile(dir, sessionID string) string {
	return strings.TrimSuffix(sessionFile(dir, sessionID), ".json") + ".seen"
}

package routeguard

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
)

// The headroom note puts how much of the plan is left in front of the
// agent, because an agent only budgets what it can see (BATS, "Budget-
// Aware Tool Use", arXiv 2511.17006).
//
// It travels in UserPromptSubmit's additionalContext, never in a session
// prefix (CLAUDE.md, AGENTS.md, SessionStart): Claude Code inserts that
// context where the hook fired, after everything already cached, and the
// transcript keeps it as written, so an earlier note never changes and
// never invalidates the cache. To keep the conversation from gaining a
// line on every turn it is said once per session and again only when a
// window crosses a step.

// MaxNoteLen bounds the note: one short line.
const MaxNoteLen = 120

// closeEnough is how near another window's share left must be to the
// binding one's to be named too: near enough to bind next.
const closeEnough = 25

// noteSteps are the shares left a note is said again at, crossing down.
var noteSteps = []float64{50, 25, 10}

// NoteWindow is one plan window as the note shows it.
type NoteWindow struct {
	// Name is "5h" or "week".
	Name    string
	LeftPct float64
	// ResetsAt is zero when unknown.
	ResetsAt time.Time
}

// ReadHeadroom reads the provider's plan windows from what is already on
// the machine, as ReadPressure does: the Claude status line's reading
// first, which carries resets, else the daemon's figures. Both are local
// files, so the hook stays fast; any failure reads as nothing known.
func ReadHeadroom(limitsPath string, figures boundaryledger.Dir, provider string, now time.Time) []NoteWindow {
	if provider == "anthropic" {
		if r, ok, err := claudelimits.Read(limitsPath); err == nil && ok && now.Sub(r.ObservedAt) <= pressureLimitsFreshFor {
			var out []NoteWindow
			for _, w := range []struct {
				name string
				win  *claudelimits.Window
			}{{"5h", r.FiveHour}, {"week", r.SevenDay}} {
				// A window that has reset since says nothing now.
				if w.win == nil || (w.win.ResetsAt > 0 && !time.Unix(w.win.ResetsAt, 0).After(now)) {
					continue
				}
				nw := NoteWindow{Name: w.name, LeftPct: math.Max(0, 100-w.win.UsedPct)}
				if w.win.ResetsAt > 0 {
					nw.ResetsAt = time.Unix(w.win.ResetsAt, 0).UTC()
				}
				out = append(out, nw)
			}
			return out
		}
	}
	fig, ok, err := figures.GetFigures()
	if err != nil || !ok || now.Sub(fig.ObservedAt) > pressureFiguresFreshFor {
		return nil
	}
	var out []NoteWindow
	for _, name := range []string{"5h", "week"} {
		if left, ok := fig.PlanLeftPct[provider][name]; ok {
			out = append(out, NoteWindow{Name: name, LeftPct: math.Max(0, left)})
		}
	}
	return out
}

// binding is the window with the least left, then any other within
// closeEnough of it, least left first.
func binding(ws []NoteWindow) []NoteWindow {
	if len(ws) == 0 {
		return nil
	}
	sorted := append([]NoteWindow(nil), ws...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].LeftPct < sorted[j].LeftPct })
	out := sorted[:1]
	for _, w := range sorted[1:] {
		if w.LeftPct-sorted[0].LeftPct <= closeEnough {
			out = append(out, w)
		}
	}
	return out
}

// HeadroomNote is the one-line note for the binding windows, or "" when
// none is known. Resets are clock times in loc: within a day the time,
// else the weekday and time.
func HeadroomNote(ws []NoteWindow, now time.Time, loc *time.Location) string {
	shown := binding(ws)
	if len(shown) == 0 {
		return ""
	}
	parts := make([]string, 0, len(shown))
	for _, w := range shown {
		label := w.Name
		if label == "5h" {
			label = "5h window"
		}
		p := fmt.Sprintf("%s %.0f%% left", label, math.Round(w.LeftPct))
		if !w.ResetsAt.IsZero() && w.ResetsAt.After(now) {
			at := w.ResetsAt.In(loc)
			if w.ResetsAt.Sub(now) < 24*time.Hour {
				p += " (resets " + at.Format("15:04") + ")"
			} else {
				p += " (resets " + at.Format("Mon 15:04") + ")"
			}
		}
		parts = append(parts, p)
	}
	note := "Plan headroom: " + strings.Join(parts, ", ") + "."
	if len(note) > MaxNoteLen {
		// The binding window alone always fits.
		note = "Plan headroom: " + parts[0] + "."
	}
	return note
}

// step is the band a share left falls in: 0 above 50%, then one more for
// each of 50, 25 and 10 it is at or under.
func step(left float64) int {
	n := 0
	for _, s := range noteSteps {
		if left <= s {
			n++
		}
	}
	return n
}

// noteState is what a session was last told.
type noteState struct {
	Steps map[string]int `json:"steps"`
}

func notePath(dir, session string) string {
	name := session
	if name == "" {
		name = "unknown"
	}
	return filepath.Join(dir, "headroom-"+filepath.Base(name)+".json")
}

// DecideHeadroomNote returns the note and true when it is due in this
// session: on its first turn with a window known, and again when a shown
// window crosses a step or the binding windows change. It records what
// it said in dir; with no dir it says the note every time.
func DecideHeadroomNote(dir, session string, ws []NoteWindow, now time.Time, loc *time.Location) (string, bool) {
	note := HeadroomNote(ws, now, loc)
	if note == "" {
		return "", false
	}
	cur := noteState{Steps: map[string]int{}}
	for _, w := range binding(ws) {
		cur.Steps[w.Name] = step(w.LeftPct)
	}
	if dir == "" {
		return note, true
	}
	var prev noteState
	if b, err := os.ReadFile(notePath(dir, session)); err == nil { //nolint:gosec // TokenOps' own state dir
		_ = json.Unmarshal(b, &prev)
	}
	if sameSteps(prev.Steps, cur.Steps) {
		return "", false
	}
	if err := os.MkdirAll(dir, 0o755); err == nil {
		if b, err := json.Marshal(cur); err == nil {
			if os.WriteFile(notePath(dir, session), b, 0o600) == nil {
				_ = PruneSessions(dir, now)
			}
		}
	}
	return note, true
}

func sameSteps(a, b map[string]int) bool {
	if len(a) != len(b) || a == nil {
		return false
	}
	for k, v := range b {
		if pv, ok := a[k]; !ok || pv != v {
			return false
		}
	}
	return true
}

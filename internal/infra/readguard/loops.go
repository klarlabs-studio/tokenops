package readguard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Loop tips are the read hook's second job: noticing when the agent goes
// round in circles, and saying so at the tool call where it happens
// rather than after the turn, where a Stop hook's line is easily missed.
//
// Two patterns are recognised, each over the last LoopWindow tool calls
// the hook saw in an agent's context:
//
//   - re-read churn: the same file read LoopRepeats times, changed or
//     not. A ranged read counts as the same read only when its range is
//     the same, so paging through a large file in parts is not churn.
//   - a search loop: the same Grep or Glob (tool, pattern, path, and for
//     Grep its glob and type filters) run LoopRepeats times with no edit
//     in between.
//
// "No edit" means no Edit, MultiEdit, Write or NotebookEdit call that the
// hook saw. The hook is installed on those tools for exactly this reason,
// but it cannot see a file changed by a Bash command or by the operator,
// so a search loop here is "the same search, and nothing else changed
// that the hook can see". It is a prompt to step back, not a verdict.
//
// Each tip is given once per file or search per agent context, and the
// state holds only hashes of paths and patterns: the tip names them from
// the call in hand.

// The loop windowing thresholds.
const (
	// LoopWindow is how many of an agent's most recent tool calls the
	// hook keeps to look for loops in.
	LoopWindow = 10
	// LoopRepeats is how many times the same read or search must occur
	// within the window before the hook says something.
	LoopRepeats = 3
	// maxPatternRunes bounds how much of a search pattern a tip quotes.
	maxPatternRunes = 60
)

// The loop tip kinds, as the coach's ledger records them.
const (
	TipRereadChurn = "reread_churn"
	TipSearchLoop  = "search_loop"
)

// CallKind is what a tool call is to the loop detector.
type CallKind string

// The call kinds.
const (
	CallRead   CallKind = "read"
	CallSearch CallKind = "search"
	CallEdit   CallKind = "edit"
)

// Call is one tool call the hook saw, reduced to what loop detection and
// the tip's wording need. It never carries file contents or prompt text.
type Call struct {
	Kind CallKind
	// Tool is the tool's name ("Grep", "Glob") for a search.
	Tool string
	// Path is the file read, or the directory a search ran in.
	Path string
	// Range is a ranged read's offset and limit as sent; empty for a
	// full read.
	Range string
	// Pattern, Glob and Type are a search's own input.
	Pattern, Glob, Type string
	// Cwd is the session's working directory, for shortening paths.
	Cwd string
}

// LoopTip is a loop the hook noticed and may tell the agent about.
type LoopTip struct {
	Kind  string
	Count int
	call  Call
}

type loopEntry struct {
	Kind CallKind `json:"k"`
	Key  string   `json:"key,omitempty"`
}

type loopState struct {
	Recent []loopEntry     `json:"recent"`
	Told   map[string]bool `json:"told,omitempty"`
	// Pending are tips given and not yet settled by what the agent did
	// next.
	Pending []pendingTip `json:"pending,omitempty"`
}

// SettleAfter is how many further tool calls in the agent's context a
// loop tip waits for the loop to recur. None that repeat it is following
// the tip.
const SettleAfter = 5

// pendingTip is a loop tip waiting to be settled. Key is hashed, like the
// window; the tip's ID is the ledger's.
type pendingTip struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Key   string `json:"key"`
	Calls int    `json:"calls,omitempty"`
}

// TipOutcome is what became of a loop tip given earlier: followed, or
// ignored with what the agent did instead. Instead names the file or the
// tool from the call in hand, never contents.
type TipOutcome struct {
	ID       string
	Kind     string
	Followed bool
	Instead  string
	Evidence string
}

// Observation is what one tool call told the loop detector.
type Observation struct {
	// Tip is the loop the call completes, and Spoke whether it was given
	// (with ID, the ledger's name for it).
	Tip   LoopTip
	Spoke bool
	ID    string
	// Settled are earlier tips this call settled.
	Settled []TipOutcome
}

// ObserveCall records one tool call in the agent context's window and
// returns the loop tip it completes, if any. speak is asked only when a
// tip is due and not yet given; a tip it declines is not marked as given,
// so it can still be given later when the coach may speak. A nil speak
// never speaks. Like the guard itself it fails open: unreadable state is
// an empty window.
func ObserveCall(dir, sessionID, agentID string, c Call, speak func(kind string) bool) (LoopTip, bool) {
	var gate func(LoopTip) bool
	if speak != nil {
		gate = func(t LoopTip) bool { return speak(t.Kind) }
	}
	o := Observe(dir, sessionID, agentID, c, gate, nil)
	return o.Tip, o.Spoke
}

// Observe is ObserveCall that also settles the tips given earlier: a tip
// is ignored when the loop it named recurs within SettleAfter calls (the
// same file read again, the same search run again with no edit between)
// and followed when it does not, or when an edit ends a search loop.
// speak is asked with the tip, so it can weigh the object the tip names.
// newID names a tip given, to be settled later; nil settles nothing.
func Observe(dir, sessionID, agentID string, c Call, speak func(LoopTip) bool, newID func() string) Observation {
	dir = resolveDir(dir)
	_ = os.MkdirAll(dir, 0o755)
	path := loopFile(dir, scopeKey(sessionID, agentID))
	st := loadLoops(path)

	var o Observation
	o.Settled, st.Pending = settle(st.Pending, c)

	st.Recent = append(st.Recent, loopEntry{Kind: c.Kind, Key: c.countKey()})
	if len(st.Recent) > LoopWindow {
		st.Recent = append([]loopEntry(nil), st.Recent[len(st.Recent)-LoopWindow:]...)
	}

	tip, due := detectLoop(st.Recent, c)
	o.Tip = tip
	told := c.toldKey()
	if due && !st.Told[told] && speak != nil && speak(tip) {
		if st.Told == nil {
			st.Told = map[string]bool{}
		}
		st.Told[told] = true
		o.Spoke = true
		if newID != nil {
			o.ID = newID()
			st.Pending = append(st.Pending, pendingTip{ID: o.ID, Kind: tip.Kind, Key: tip.settleKey()})
		}
	}
	saveLoops(path, st)
	return o
}

// settle weighs the pending tips against the call in hand and returns
// those it settled and those still waiting.
func settle(pending []pendingTip, c Call) ([]TipOutcome, []pendingTip) {
	var out []TipOutcome
	var still []pendingTip
	for _, p := range pending {
		switch {
		case p.Kind == TipRereadChurn && c.Kind == CallRead && c.toldKey() == p.Key:
			out = append(out, TipOutcome{ID: p.ID, Kind: p.Kind, Instead: "read " + filepath.Base(c.Path) + " again"})
		case p.Kind == TipSearchLoop && c.Kind == CallSearch && c.countKey() == p.Key:
			out = append(out, TipOutcome{ID: p.ID, Kind: p.Kind, Instead: "ran the same " + c.Tool + " again"})
		case p.Kind == TipSearchLoop && c.Kind == CallEdit:
			out = append(out, TipOutcome{ID: p.ID, Kind: p.Kind, Followed: true, Evidence: "an edit followed"})
		case p.Calls+1 >= SettleAfter:
			out = append(out, TipOutcome{ID: p.ID, Kind: p.Kind, Followed: true,
				Evidence: fmt.Sprintf("not repeated in the next %d tool calls", SettleAfter)})
		default:
			p.Calls++
			still = append(still, p)
		}
	}
	return out, still
}

// settleKey is what a later call must match to repeat the loop the tip
// named: the file whatever its range, or the same search.
func (t LoopTip) settleKey() string {
	if t.Kind == TipRereadChurn {
		return t.call.toldKey()
	}
	return t.call.countKey()
}

// Object is the one thing the tip names, for the coach's ledger: a file's
// base name, or the search's tool and a bounded slice of its pattern.
func (t LoopTip) Object() string {
	switch t.Kind {
	case TipRereadChurn:
		return filepath.Base(t.call.Path)
	case TipSearchLoop:
		return t.call.Tool + " " + quotePattern(t.call.Pattern)
	}
	return ""
}

// detectLoop reports whether the newest call, already in recent, closes
// a loop.
func detectLoop(recent []loopEntry, c Call) (LoopTip, bool) {
	key := c.countKey()
	n := 0
	switch c.Kind {
	case CallRead:
		for _, e := range recent {
			if e.Kind == CallRead && e.Key == key {
				n++
			}
		}
		return LoopTip{Kind: TipRereadChurn, Count: n, call: c}, n >= LoopRepeats
	case CallSearch:
		// Count back to the last edit: a search repeated after a change
		// is checking the change, not going round.
		for i := len(recent) - 1; i >= 0 && recent[i].Kind != CallEdit; i-- {
			if recent[i].Kind == CallSearch && recent[i].Key == key {
				n++
			}
		}
		return LoopTip{Kind: TipSearchLoop, Count: n, call: c}, n >= LoopRepeats
	}
	return LoopTip{}, false
}

// countKey identifies the call for counting: the same file and range, or
// the same search. Hashed, so the state on disk names nothing.
func (c Call) countKey() string {
	switch c.Kind {
	case CallRead:
		return hashKey("read", c.Path, c.Range)
	case CallSearch:
		return hashKey("search", c.Tool, normalizePattern(c.Pattern), cleanSearchPath(c.Path), c.Glob, c.Type)
	}
	return ""
}

// toldKey identifies what a tip is about for once-per-session: the file,
// whatever range was read, or the search.
func (c Call) toldKey() string {
	if c.Kind == CallRead {
		return hashKey("read", c.Path)
	}
	return c.countKey()
}

// Message is the note given to the agent. It names the file or the search
// from the agent's own tool input and quotes no more than a bounded slice
// of a pattern.
func (t LoopTip) Message() string {
	c := t.call
	switch t.Kind {
	case TipRereadChurn:
		return fmt.Sprintf("tokenops: you have read %s %d times in the last %d tool calls. "+
			"Keep the part you need in view, or say what you are looking for in it, instead of reading it again.",
			nameFile(c.Path, c.Cwd), t.Count, LoopWindow)
	case TipSearchLoop:
		return fmt.Sprintf("tokenops: %s has run %d times in the last %d tool calls with no edit in between. "+
			"If it is not finding what you need, change the pattern or the path, or say what you are looking for.",
			nameSearch(c), t.Count, LoopWindow)
	}
	return ""
}

// nameFile is a file's base name with a short path to it, e.g.
// "readguard.go (internal/cli/readguard.go)".
func nameFile(path, cwd string) string {
	base := filepath.Base(path)
	short := shortPath(path, cwd)
	if short == base {
		return base
	}
	return base + " (" + short + ")"
}

// nameSearch is how a tip refers to a search, e.g.
// `Grep for "foo" in internal/cli`.
func nameSearch(c Call) string {
	s := c.Tool + " for " + quotePattern(c.Pattern)
	if c.Path != "" {
		s += " in " + shortPath(c.Path, c.Cwd)
	}
	if c.Glob != "" {
		s += " (glob " + quotePattern(c.Glob) + ")"
	}
	if c.Type != "" {
		s += " (type " + quotePattern(c.Type) + ")"
	}
	return s
}

// shortPath is path relative to cwd when it lies inside it, home-relative
// when it lies under the home directory, and otherwise as given; a long
// result keeps only its last few segments.
func shortPath(path, cwd string) string {
	p := filepath.Clean(path)
	if cwd != "" && filepath.IsAbs(p) {
		if rel, err := filepath.Rel(cwd, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			p = rel
		}
	}
	if filepath.IsAbs(p) {
		if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
			p = "~" + p[len(home):]
		}
	}
	const maxLen, keep = 60, 4
	if len(p) > maxLen {
		parts := strings.Split(p, string(filepath.Separator))
		if len(parts) > keep {
			p = "…" + string(filepath.Separator) + strings.Join(parts[len(parts)-keep:], string(filepath.Separator))
		}
	}
	return p
}

// quotePattern quotes a pattern for a tip, with control characters made
// spaces and anything past maxPatternRunes cut.
func quotePattern(p string) string {
	p = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, p)
	if utf8.RuneCountInString(p) > maxPatternRunes {
		p = string([]rune(p)[:maxPatternRunes]) + "…"
	}
	return `"` + p + `"`
}

// normalizePattern makes the same search typed twice compare equal: outer
// whitespace dropped. The pattern is otherwise kept, since case and inner
// spacing change what a regular expression matches.
func normalizePattern(p string) string { return strings.TrimSpace(p) }

// cleanSearchPath makes "", "." and "./" the same directory.
func cleanSearchPath(p string) string {
	if strings.TrimSpace(p) == "" {
		return "."
	}
	return filepath.Clean(p)
}

func hashKey(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:12])
}

func loopFile(dir, scope string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(scope)
	if safe == "" {
		safe = "default"
	}
	return filepath.Join(dir, "loops-"+safe+".json")
}

func loadLoops(path string) loopState {
	var st loopState
	b, err := os.ReadFile(path) //nolint:gosec // our own state file
	if err != nil || json.Unmarshal(b, &st) != nil {
		return loopState{}
	}
	return st
}

// saveLoops writes atomically, like saveSession: a lost update under
// parallel hooks only misses a loop, never corrupts the window.
func saveLoops(path string, st loopState) {
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil { //nolint:gosec // hashes only
		_ = os.Rename(tmp, path)
	}
}

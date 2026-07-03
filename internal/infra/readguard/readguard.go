// Package readguard is the reclamation half of the Read analysis: a Claude
// Code PreToolUse hook that prevents redundant re-reads at the source — the
// only place tokens can actually be reclaimed for a client (like Claude Code
// on a subscription) whose traffic never reaches the tokenops proxy.
//
// A re-read is redundant when the SAME file is read in FULL (no offset/limit)
// more than once in a session and the file has not changed since the last
// read (cheap mtime+size fingerprint — the file is never opened). Ranged
// reads are always allowed; they are intentional. In observe mode the guard
// only logs what it would block (zero risk, real reclamation numbers from
// live sessions); in active mode it denies the re-read and tells the model
// to use the copy already in its context.
package readguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Mode selects observe (log only) vs active (deny redundant re-reads).
type Mode string

const (
	ModeObserve Mode = "observe"
	ModeActive  Mode = "active"
)

// ParseMode maps a token to a Mode, defaulting to observe.
func ParseMode(s string) Mode {
	if strings.EqualFold(strings.TrimSpace(s), string(ModeActive)) {
		return ModeActive
	}
	return ModeObserve
}

// Action is what the guard did with a read.
type Action string

const (
	ActionAllow      Action = "allow"
	ActionWouldBlock Action = "would_block" // observe mode, redundant
	ActionBlocked    Action = "blocked"     // active mode, redundant
)

// Decision is the guard's verdict for one Read.
type Decision struct {
	Block     bool
	Reason    string
	Action    Action
	EstTokens int64
}

// fingerprint is a cheap unchanged-since check: a file is "unchanged" when
// its modtime and size both match the last read. The file is never opened.
type fingerprint struct {
	ModUnix int64 `json:"mod"`
	Size    int64 `json:"size"`
}

type pathState struct {
	FP    fingerprint `json:"fp"`
	Reads int         `json:"reads"`
}

type sessionState struct {
	Paths map[string]pathState `json:"paths"`
}

// ledgerEvent is one appended reclamation record.
type ledgerEvent struct {
	TS        time.Time `json:"ts"`
	Mode      Mode      `json:"mode"`
	Session   string    `json:"session"`
	Path      string    `json:"path"`
	Action    Action    `json:"action"`
	EstTokens int64     `json:"est_tokens"`
}

// Evaluate is the pure-ish decision + side effects for one Read. dir is the
// state/ledger root (defaults to ~/.tokenops/read-guard when empty). It
// stats the file for the fingerprint, updates per-session state, appends a
// ledger event, and returns the decision. now is injected for tests.
func Evaluate(dir, sessionID, filePath string, ranged bool, mode Mode, now time.Time) Decision {
	dir = resolveDir(dir)
	_ = os.MkdirAll(dir, 0o755)

	estTokens := estTokensForFile(filePath)

	// Ranged reads are intentional partial reads — never dedup them, but
	// still record so a later full read can be compared.
	st := loadSession(dir, sessionID)
	prev, seen := st.Paths[filePath]
	fp, ok := statFingerprint(filePath)

	redundant := !ranged && seen && ok && prev.FP == fp

	dec := Decision{Action: ActionAllow, EstTokens: estTokens}
	if redundant {
		if mode == ModeActive {
			dec.Block = true
			dec.Action = ActionBlocked
			dec.Reason = "tokenops read-guard: this file was already read in full this session and is unchanged — use the copy already in your context instead of re-reading (saves ~" + human(estTokens) + " tokens). If you need it fresh, edit then read, or read a specific range."
		} else {
			dec.Action = ActionWouldBlock
		}
	}

	// Record the current fingerprint so subsequent identical reads are also
	// caught. On a genuine change (fp differs) this refreshes the baseline.
	ps := prev
	ps.FP = fp
	ps.Reads++
	if st.Paths == nil {
		st.Paths = map[string]pathState{}
	}
	st.Paths[filePath] = ps
	saveSession(dir, sessionID, st)

	appendLedger(dir, ledgerEvent{
		TS: now.UTC(), Mode: mode, Session: sessionID, Path: filePath,
		Action: dec.Action, EstTokens: estTokens,
	})
	return dec
}

// Stats summarises the ledger.
type Stats struct {
	Events           int   `json:"events"`
	WouldBlock       int   `json:"would_block"`
	Blocked          int   `json:"blocked"`
	ReclaimableTok   int64 `json:"reclaimable_tokens"` // observe would-block sum
	ReclaimedTok     int64 `json:"reclaimed_tokens"`   // active blocked sum
	DistinctSessions int   `json:"distinct_sessions"`
}

// ReadStats reads the ledger and aggregates it.
func ReadStats(dir string) (Stats, error) {
	dir = resolveDir(dir)
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return Stats{}, nil
		}
		return Stats{}, err
	}
	defer func() { _ = f.Close() }()

	var s Stats
	sessions := map[string]struct{}{}
	dec := json.NewDecoder(f)
	for {
		var e ledgerEvent
		if err := dec.Decode(&e); err != nil {
			break
		}
		s.Events++
		sessions[e.Session] = struct{}{}
		switch e.Action {
		case ActionWouldBlock:
			s.WouldBlock++
			s.ReclaimableTok += e.EstTokens
		case ActionBlocked:
			s.Blocked++
			s.ReclaimedTok += e.EstTokens
		}
	}
	s.DistinctSessions = len(sessions)
	return s, nil
}

// --- helpers ---------------------------------------------------------------

func resolveDir(dir string) string {
	if dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".tokenops-readguard"
	}
	return filepath.Join(home, ".tokenops", "read-guard")
}

func statFingerprint(path string) (fingerprint, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return fingerprint{}, false
	}
	return fingerprint{ModUnix: st.ModTime().UnixNano(), Size: st.Size()}, true
}

func estTokensForFile(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size() / 4
}

func sessionFile(dir, sessionID string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(sessionID)
	if safe == "" {
		safe = "default"
	}
	return filepath.Join(dir, "session-"+safe+".json")
}

func loadSession(dir, sessionID string) sessionState {
	var st sessionState
	b, err := os.ReadFile(sessionFile(dir, sessionID))
	if err != nil {
		return sessionState{Paths: map[string]pathState{}}
	}
	if json.Unmarshal(b, &st) != nil || st.Paths == nil {
		st.Paths = map[string]pathState{}
	}
	return st
}

// saveSession writes state atomically (temp + rename) so parallel hook
// processes can't corrupt the file. A lost update under a race only means a
// dedup miss, never corruption.
func saveSession(dir, sessionID string, st sessionState) {
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	final := sessionFile(dir, sessionID)
	tmp := final + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, final)
	}
}

func appendLedger(dir string, e ledgerEvent) {
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_ = json.NewEncoder(f).Encode(e)
}

func human(t int64) string {
	switch {
	case t >= 1000:
		return itoa(t/1000) + "k"
	default:
		return itoa(t)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

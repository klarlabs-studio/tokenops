// Package boundaryledger keeps what the boundary pause point decided, on
// disk beside the other hooks' state (ADR 0014).
//
// The pause point runs before every tool call and must answer from memory
// in milliseconds, so it never opens the event store. It appends each
// decision it makes here as a full event envelope, and the daemon ingests
// the ledger into the store (Poller) — the pattern the Cursor stop hook
// and the status line already use. Envelope ids are unique, so re-reading
// the whole ledger is idempotent: the store drops a repeat.
//
// Beside the ledger live the small files the pause point reads by decision
// id: a pending record for each decision that asked or refused, and the
// operator's answer to it. Each is one file, written atomically, so
// parallel hook processes never see half of one.
//
// Layout under ~/.tokenops/boundaries:
//
//	decisions.jsonl      decision envelopes, newest last
//	pending/<id>.json    a decision waiting on an answer
//	answers/<id>.json    the operator's answer
//	sessions/<id>.json   per-session counters for status
//	figures.json         the daemon's plan and budget readings
//	org.json             the organisation's signed rules, as fetched
package boundaryledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Dir is the ledger's directory; empty resolves to ~/.tokenops/boundaries.
type Dir string

// DefaultDir is ~/.tokenops/boundaries.
func DefaultDir() Dir {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dir(filepath.Join(".tokenops", "boundaries"))
	}
	return Dir(filepath.Join(home, ".tokenops", "boundaries"))
}

func (d Dir) root() string {
	if d == "" {
		return string(DefaultDir())
	}
	return string(d)
}

const ledgerFile = "decisions.jsonl"

// pruneAbove is the ledger size past which an append rewrites it keeping
// only the newest keepLines. The daemon ingests every few minutes, so
// what is pruned has long been stored.
const (
	pruneAbove = 4 << 20
	keepLines  = 5000
)

var appendMu sync.Mutex

// Append adds env to the ledger. The file is opened O_APPEND and each
// envelope is one write, so concurrent hook processes interleave whole
// lines.
func (d Dir) Append(env *eventschema.Envelope) error {
	if env == nil {
		return nil
	}
	if _, ok := env.Payload.(*eventschema.DecisionEvent); !ok {
		return fmt.Errorf("boundary ledger holds decisions only, got %T", env.Payload)
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(d.root(), 0o700); err != nil {
		return err
	}
	path := filepath.Join(d.root(), ledgerFile)
	appendMu.Lock()
	defer appendMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // TokenOps' own ledger
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	if st, err := os.Stat(path); err == nil && st.Size() > pruneAbove {
		return prune(path)
	}
	return nil
}

func prune(path string) error {
	b, err := os.ReadFile(path) //nolint:gosec // TokenOps' own ledger
	if err != nil {
		return err
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	if len(lines) <= keepLines {
		return nil
	}
	kept := bytes.Join(lines[len(lines)-keepLines:], []byte("\n"))
	return writeAtomic(path, append(kept, '\n'))
}

// ledgerLine decodes one ledger line: the ledger holds decisions only,
// so the payload's type is known and the shallower Payload field takes
// it.
type ledgerLine struct {
	eventschema.Envelope
	Payload *eventschema.DecisionEvent `json:"payload"`
}

// Read returns every envelope in the ledger, oldest first. A torn or
// unreadable line is skipped, never fatal.
func (d Dir) Read() ([]*eventschema.Envelope, error) {
	f, err := os.Open(filepath.Join(d.root(), ledgerFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []*eventschema.Envelope
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var l ledgerLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.ID == "" || l.Payload == nil {
			continue
		}
		env := l.Envelope
		env.Payload = l.Payload
		out = append(out, &env)
	}
	return out, sc.Err()
}

// Decision returns the ledger's envelopes for one decision id.
func (d Dir) Decision(id string) ([]*eventschema.Envelope, error) {
	all, err := d.Read()
	if err != nil {
		return nil, err
	}
	var out []*eventschema.Envelope
	for _, env := range all {
		if env.Correlation.Decision == id {
			out = append(out, env)
		}
	}
	return out, nil
}

// Pending is a decision waiting on an answer: what asked, under which
// rule, and what was crossed. It holds no prompt or tool input.
type Pending struct {
	ID        string    `json:"id"`
	Rule      string    `json:"rule"`
	Require   string    `json:"require"`
	Crossed   string    `json:"crossed"`
	Action    string    `json:"action"`
	Harness   string    `json:"harness"`
	SessionID string    `json:"session_id"`
	AgentID   string    `json:"agent_id,omitempty"`
	At        time.Time `json:"at"`
	// ToolUseID is the call an attended ask was raised on, so the next
	// call can read the answer the operator gave at the prompt.
	ToolUseID string `json:"tool_use_id,omitempty"`
	// Crossings are the figures that crossed the rule, kept for an
	// approval routed to the organisation.
	Crossings []boundaries.Crossing `json:"crossings,omitempty"`
	// RoutedAt is when an organisation rule's approval request reached
	// the team plane.
	RoutedAt *time.Time `json:"routed_at,omitempty"`
}

// Answer is the operator's answer to a pending decision.
type Answer struct {
	ID        string    `json:"id"`
	Rule      string    `json:"rule"`
	SessionID string    `json:"session_id"`
	Approved  bool      `json:"approved"`
	Scope     string    `json:"scope"`
	Approver  string    `json:"approver"`
	Method    string    `json:"method"`
	At        time.Time `json:"at"`
}

// Scopes an answer can have.
const (
	ScopeOnce    = "once"
	ScopeSession = "session"
)

// answerTTL bounds how long an answer is honoured. A decision id is
// already per session, so an answer cannot leak into another session;
// the bound keeps a session-long approval from outliving the work it was
// given for.
const answerTTL = 24 * time.Hour

func (d Dir) idPath(kind, id string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid decision id %q", id)
	}
	return filepath.Join(d.root(), kind, id+".json"), nil
}

// PutPending records a pending decision.
func (d Dir) PutPending(p Pending) error { return d.putJSON("pending", p.ID, p) }

// GetPending reads a pending decision.
func (d Dir) GetPending(id string) (Pending, bool, error) {
	var p Pending
	ok, err := d.getJSON("pending", id, &p)
	return p, ok, err
}

// DeletePending removes a pending decision once its answer is recorded,
// so the next time the rule holds it is asked afresh.
func (d Dir) DeletePending(id string) error {
	path, err := d.idPath("pending", id)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ListPending returns every pending decision, newest first.
func (d Dir) ListPending() ([]Pending, error) {
	entries, err := os.ReadDir(filepath.Join(d.root(), "pending"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Pending
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if p, found, err := d.GetPending(id); err == nil && found {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b Pending) int { return b.At.Compare(a.At) })
	return out, nil
}

// PutAnswer records the operator's answer.
func (d Dir) PutAnswer(a Answer) error { return d.putJSON("answers", a.ID, a) }

// GetAnswer reads the answer to id, if one is still honoured at now.
func (d Dir) GetAnswer(id string, now time.Time) (Answer, bool, error) {
	var a Answer
	ok, err := d.getJSON("answers", id, &a)
	if err != nil || !ok {
		return Answer{}, false, err
	}
	if now.Sub(a.At) > answerTTL {
		return Answer{}, false, nil
	}
	return a, true, nil
}

// ConsumeAnswer spends a one-off answer. Exactly one caller wins when
// parallel tool calls race for it: the rename succeeds once.
func (d Dir) ConsumeAnswer(id string) (bool, error) {
	path, err := d.idPath("answers", id)
	if err != nil {
		return false, err
	}
	err = os.Rename(path, path+".used")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_ = os.Remove(path + ".used")
	return true, nil
}

func (d Dir) putJSON(kind, id string, v any) error {
	path, err := d.idPath(kind, id)
	if err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, b)
}

func (d Dir) getJSON(kind, id string, v any) (bool, error) {
	path, err := d.idPath(kind, id)
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(path) //nolint:gosec // TokenOps' own state, id validated
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, err
	}
	return true, nil
}

// Session is the per-session counters status reads: how often each rule
// matched, and which decision stages were already recorded, so a rule
// that matches every call records its decision once, not once per call.
//
// The session's file holds the decision stages; the matches are counted
// in a log beside it (AddMatches), and Sessions adds the two. Matches in
// the file are counts written before the log existed.
type Session struct {
	Matches  map[string]int    `json:"matches,omitempty"`
	Recorded map[string]string `json:"recorded,omitempty"`
	LastAt   time.Time         `json:"last_at"`
}

// GetSession reads a session's file — its decision stages; a missing file
// is empty. Its match counts are Sessions'.
func (d Dir) GetSession(id string) (Session, error) {
	var s Session
	if _, err := d.getJSON("sessions", sessionKey(id), &s); err != nil {
		return Session{}, err
	}
	return s, nil
}

// PutSession writes a session's file. A lost update under a race costs a
// decision stage being recorded twice, never a decision.
func (d Dir) PutSession(id string, s Session) error {
	return d.putJSON("sessions", sessionKey(id), s)
}

// matchLine is one evaluation's matches in a session's match log, or,
// once the log was folded, the totals of the lines before it.
type matchLine struct {
	At     time.Time      `json:"at"`
	Rules  []string       `json:"rules,omitempty"`
	Counts map[string]int `json:"counts,omitempty"`
}

// matchLogExt names a session's match log beside its file.
const matchLogExt = ".matches"

// matchLogCap is the size past which a match log is folded into one line
// of totals: about a thousand matched calls.
const matchLogCap = 64 << 10

// AddMatches counts one evaluation's matched rules for a session. The
// pause point runs before every tool call, and rewriting the session's
// file for each match (a temporary file and a rename) cost it a
// millisecond or more, several when the disk was busy. One appended line
// is one write: a short write to a file opened for appending lands whole,
// so parallel tool calls each count, where rewriting the file let the
// last writer drop the others' counts.
func (d Dir) AddMatches(id string, rules []string, at time.Time) error {
	if len(rules) == 0 {
		return nil
	}
	b, err := json.Marshal(matchLine{At: at.UTC(), Rules: rules})
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := filepath.Join(d.root(), "sessions", sessionKey(id)+matchLogExt)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // TokenOps' own state, key sanitised
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		f, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // as above
	}
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	st, serr := f.Stat()
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	if serr == nil && st.Size() > matchLogCap {
		return d.foldMatchLog(sessionKey(id))
	}
	return nil
}

// foldMatchLog rewrites a session's match log as one line of its totals.
// A match appended while it folds can be lost: a count, never a decision.
func (d Dir) foldMatchLog(key string) error {
	s := d.readMatchLog(key)
	b, err := json.Marshal(matchLine{At: s.LastAt, Counts: s.Matches})
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(d.root(), "sessions", key+matchLogExt), append(b, '\n'))
}

// Sessions returns every session's counters: its file's decision stages
// and earlier counts, plus the matches its log counts.
func (d Dir) Sessions() (map[string]Session, error) {
	entries, err := os.ReadDir(filepath.Join(d.root(), "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Session{}
	for _, e := range entries {
		if key, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			var s Session
			if found, err := d.getJSON("sessions", key, &s); err == nil && found {
				out[key] = mergeSession(out[key], s)
			}
			continue
		}
		if key, ok := strings.CutSuffix(e.Name(), matchLogExt); ok {
			out[key] = mergeSession(out[key], d.readMatchLog(key))
		}
	}
	return out, nil
}

// readMatchLog counts a session's match log. A line that does not parse —
// one a crash cut short — is skipped.
func (d Dir) readMatchLog(key string) Session {
	s := Session{Matches: map[string]int{}}
	f, err := os.Open(filepath.Join(d.root(), "sessions", key+matchLogExt)) //nolint:gosec // TokenOps' own state
	if err != nil {
		return s
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var l matchLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		for _, r := range l.Rules {
			s.Matches[r]++
		}
		for r, n := range l.Counts {
			s.Matches[r] += n
		}
		if l.At.After(s.LastAt) {
			s.LastAt = l.At
		}
	}
	return s
}

// mergeSession adds b's counts to a's and keeps the later LastAt and
// every recorded stage.
func mergeSession(a, b Session) Session {
	if a.Matches == nil {
		a.Matches = map[string]int{}
	}
	for r, n := range b.Matches {
		a.Matches[r] += n
	}
	if len(b.Recorded) > 0 {
		if a.Recorded == nil {
			a.Recorded = map[string]string{}
		}
		maps.Copy(a.Recorded, b.Recorded)
	}
	if b.LastAt.After(a.LastAt) {
		a.LastAt = b.LastAt
	}
	return a
}

func sessionKey(id string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(id)
	if safe == "" {
		return "default"
	}
	return safe
}

// Figures are the readings the pause point compares but cannot compute
// without the event store: each plan window's headroom and each budget's
// share used. The daemon writes them every minute; the pause point reads
// the one file.
type Figures struct {
	ObservedAt time.Time `json:"observed_at"`
	// PlanLeftPct is each provider's share left, by window ("5h", "week").
	PlanLeftPct map[string]map[string]float64 `json:"plan_left_pct,omitempty"`
	// BudgetUsedPct is each configured budget's share used in its current
	// window, by budget name. A budget with several windows has one entry
	// per window ("name:daily") and one by its name, its fullest window.
	BudgetUsedPct map[string]float64 `json:"budget_used_pct,omitempty"`
	// Budgets are the same windows in full, for the surfaces that show
	// them: the coach's tips and the glance.
	Budgets []BudgetReading `json:"budgets,omitempty"`
}

// BudgetReading is one budget window as the daemon measured it. Figures
// only: a name the operator gave, amounts and shares.
type BudgetReading struct {
	// Key is the window's figure key in BudgetUsedPct.
	Key    string `json:"key"`
	Budget string `json:"budget"`
	Window string `json:"window"`
	// Mode is soft (notify) or hard (enforced by Rule).
	Mode string `json:"mode"`
	// Basis is spend, equivalent or tokens; Used and Limit are in its unit.
	Basis string `json:"basis,omitempty"`
	// Provider is the provider whose bill the window counts (a registry
	// id), empty for every provider's.
	Provider string  `json:"provider,omitempty"`
	Used     float64 `json:"used"`
	Limit    float64 `json:"limit"`
	UsedPct  float64 `json:"used_pct"`
	// WarnPct and CritPct are the window's alert thresholds, in percent.
	WarnPct     float64   `json:"warn_pct"`
	CritPct     float64   `json:"crit_pct"`
	WindowStart time.Time `json:"window_start"`
	ResetsAt    time.Time `json:"resets_at"`
	// Rule is the boundary a hard window is enforced by, and Enforced
	// whether it binds or is still in shadow, and Require what it requires
	// at the limit (ask or deny).
	Rule     string `json:"rule,omitempty"`
	Enforced bool   `json:"enforced,omitempty"`
	Require  string `json:"require,omitempty"`
}

const figuresFile = "figures.json"

// PutFigures replaces the readings.
func (d Dir) PutFigures(f Figures) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.root(), 0o700); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(d.root(), figuresFile), b)
}

// GetFigures reads the readings; a missing file is not an error.
func (d Dir) GetFigures() (Figures, bool, error) {
	b, err := os.ReadFile(filepath.Join(d.root(), figuresFile)) //nolint:gosec // TokenOps' own state
	if errors.Is(err, os.ErrNotExist) {
		return Figures{}, false, nil
	}
	if err != nil {
		return Figures{}, false, err
	}
	var f Figures
	if err := json.Unmarshal(b, &f); err != nil {
		return Figures{}, false, err
	}
	return f, true, nil
}

const orgFile = "org.json"

// PutOrg replaces the cached organisation rules: the signed document as
// the server sent it, verified again every time it is read.
func (d Dir) PutOrg(signed []byte) error {
	if err := os.MkdirAll(d.root(), 0o700); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(d.root(), orgFile), signed)
}

// GetOrg reads the cached organisation rules; a missing file is not an
// error.
func (d Dir) GetOrg() ([]byte, bool, error) {
	b, err := os.ReadFile(filepath.Join(d.root(), orgFile)) //nolint:gosec // TokenOps' own state
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

// OrgModTime is when the cached rules last changed, for callers that
// keep them in memory; zero when there are none.
func (d Dir) OrgModTime() time.Time {
	info, err := os.Stat(filepath.Join(d.root(), orgFile))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// RemoveOrg forgets the organisation's rules, as leaving a team does.
func (d Dir) RemoveOrg() error {
	err := os.Remove(filepath.Join(d.root(), orgFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Package moveverify keeps the evidence and decisions behind judging the
// coach's autonomous model moves (moveverdict) under
// ~/.tokenops/coach/move-verify:
//
//   - sessions/<id>.json: one file per Claude Code session, holding its
//     settled subagent observations and its default-model observation.
//     Each session's Stop hook writes only its own file, so concurrent
//     sessions never race on the evidence.
//   - retractions.json: the retraction decisions, written under a file
//     lock, and read on the hook path to hold a retracted move.
//
// Nothing here stores prompt text or file contents: observations hold
// task kinds, model families, times and signal counts.
package moveverify

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/moveverdict"
	"go.klarlabs.de/tokenops/internal/infra/filelock"
)

// Store is the move-verify directory.
type Store struct {
	Dir string
}

// Default is the store under the operator's home directory.
func Default() (Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Dir: filepath.Join(home, ".tokenops", "coach", "move-verify")}, nil
}

// SessionFile is one session's evidence.
type SessionFile struct {
	Session    string    `json:"session"`
	Transcript string    `json:"transcript,omitempty"`
	Updated    time.Time `json:"updated"`
	// Pending marks subagent calls not yet settled: the file is read
	// again later, even after the session has ended.
	Pending bool `json:"pending,omitempty"`
	// LeverChecked records that the session's start was checked against
	// the default-model lever; Lever is the move it started under, nil
	// when none. Decided once, at the session's first turn.
	LeverChecked bool              `json:"lever_checked,omitempty"`
	Lever        *moveverdict.Move `json:"lever,omitempty"`
	// Calls are the settled subagent observations; Start the session's
	// own observation for the default-model lever.
	Calls []moveverdict.Observation `json:"calls,omitempty"`
	Start *moveverdict.Observation  `json:"start,omitempty"`
}

// Merge adds settled observations not already held.
func (f *SessionFile) Merge(obs []moveverdict.Observation) {
	have := make(map[string]bool, len(f.Calls))
	for _, o := range f.Calls {
		have[o.Ref] = true
	}
	for _, o := range obs {
		if !have[o.Ref] {
			f.Calls = append(f.Calls, o)
			have[o.Ref] = true
		}
	}
}

// Observations is everything the file holds.
func (f SessionFile) Observations() []moveverdict.Observation {
	out := append([]moveverdict.Observation(nil), f.Calls...)
	if f.Start != nil && f.Start.Trials > 0 {
		out = append(out, *f.Start)
	}
	return out
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (s Store) sessionPath(session string) (string, bool) {
	if s.Dir == "" || !safeName.MatchString(session) {
		return "", false
	}
	return filepath.Join(s.Dir, "sessions", session+".json"), true
}

// LoadSession is the session's file, empty when there is none.
func (s Store) LoadSession(session string) SessionFile {
	f := SessionFile{Session: session}
	p, ok := s.sessionPath(session)
	if !ok {
		return f
	}
	if b, err := os.ReadFile(p); err == nil { //nolint:gosec // tokenops' own state
		_ = json.Unmarshal(b, &f)
	}
	return f
}

// SaveSession writes the session's file.
func (s Store) SaveSession(f SessionFile) error {
	p, ok := s.sessionPath(f.Session)
	if !ok {
		return errors.New("moveverify: no store or unusable session id")
	}
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return writeAtomic(p, b)
}

// Sessions loads every session file written within the lookback. With
// prune it also removes older ones: evidence the judge no longer reads is
// not kept. Readers that only report (status, explain) pass false.
func (s Store) Sessions(now time.Time, prune bool) []SessionFile {
	if s.Dir == "" {
		return nil
	}
	dir := filepath.Join(s.Dir, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SessionFile
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > moveverdict.Lookback+moveverdict.MaxRun {
			if prune {
				_ = os.Remove(p)
			}
			continue
		}
		var f SessionFile
		if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &f) == nil { //nolint:gosec // tokenops' own state
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Session < out[j].Session })
	return out
}

// State is the retraction file.
type State struct {
	Retractions []moveverdict.Retraction `json:"retractions,omitempty"`
	// JudgedAt is when the evidence was last judged, so the judge runs
	// at most every few minutes however many sessions end turns.
	JudgedAt time.Time `json:"judged_at,omitzero"`
}

func (s Store) statePath() string { return filepath.Join(s.Dir, "retractions.json") }

// Load reads the retraction state; a missing or unreadable file is an
// empty one.
func (s Store) Load() State {
	var st State
	if s.Dir == "" {
		return st
	}
	if b, err := os.ReadFile(s.statePath()); err == nil { //nolint:gosec // tokenops' own state
		_ = json.Unmarshal(b, &st)
	}
	return st
}

// Update changes the retraction state under an exclusive lock, so two
// sessions judging at once cannot lose each other's decisions.
func (s Store) Update(change func(*State) error) error {
	if s.Dir == "" {
		return errors.New("moveverify: no store")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(s.Dir, ".lock"), os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // tokenops' own state
	if err != nil {
		return err
	}
	defer func() { _ = lf.Close() }()
	unlock, err := filelock.Lock(lf)
	if err != nil {
		return err
	}
	defer unlock()
	st := s.Load()
	if err := change(&st); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.statePath(), b)
}

// Retracted reports, for the hook path, whether a move kind (task kind,
// from-family, to-family) is retracted. It reads one small file once.
func (s Store) Retracted() func(kind, from, to string) bool {
	active := map[moveverdict.Key]bool{}
	for _, r := range s.Load().Retractions {
		if r.Active() {
			active[r.Key] = true
		}
	}
	return func(kind, from, to string) bool {
		return active[moveverdict.Key{Kind: kind, From: from, To: to}]
	}
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(fs.FileMode(0o600)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

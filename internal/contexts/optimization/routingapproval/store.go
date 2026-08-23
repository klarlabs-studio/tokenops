// Package routingapproval persists the operator's answers about model
// upgrades the router refused to make on its own.
//
// It exists because the two halves of the conversation live in different
// processes: the daemon (`tokenops start`) runs the proxy that refuses an
// upgrade, while the MCP server (`tokenops serve`) is where the operator
// is actually asked. The record has to outlive both.
//
// The file is an append-only log rather than mutable state. Proposals and
// decisions are both appended, and current state is a fold over the log —
// which makes concurrent writers safe without locking (POSIX guarantees
// atomicity for small O_APPEND writes) and leaves an audit trail of who
// decided what, when.
package routingapproval

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Kind distinguishes the two record types in the log.
type Kind string

// Record kinds.
const (
	KindProposed Kind = "proposed"
	KindDecided  Kind = "decided"
)

// Record is one line of the log.
type Record struct {
	Kind Kind      `json:"kind"`
	At   time.Time `json:"at"`
	// Key identifies the route: provider|from_model|to_model.
	Key      string `json:"key"`
	Provider string `json:"provider"`
	From     string `json:"from_model"`
	To       string `json:"to_model"`
	// Preferred is the operator's ceiling at the time of proposal, and
	// the alternative offered to them.
	Preferred string `json:"preferred_model,omitempty"`
	// DeltaUSD is the per-request list-price increase the upgrade would
	// cost, when it could be computed.
	DeltaUSD float64 `json:"delta_usd,omitempty"`
	Priced   bool    `json:"priced,omitempty"`
	Reason   string  `json:"reason,omitempty"`
	// Decision is set on KindDecided records.
	Decision string `json:"decision,omitempty"`
	// ChosenModel records what the operator picked. For a denial this is
	// their preferred model — the route they kept.
	ChosenModel string `json:"chosen_model,omitempty"`
	// Seen counts how many times the route was proposed before an answer
	// arrived, so a stale proposal can be told from a hot one.
	Seen int64 `json:"seen,omitempty"`
}

// State is the current status of one route, folded from the log.
type State struct {
	Record
	Decided bool
}

// Store is an append-only proposal log at Path.
type Store struct {
	path string
}

// Open returns a Store backed by path, creating the parent directory if
// needed. A missing file is not an error — it reads as an empty log.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("routingapproval: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("routingapproval: create dir: %w", err)
	}
	return &Store{path: path}, nil
}

// DefaultPath is the log's conventional location next to the event store.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("routingapproval: resolve home: %w", err)
	}
	return filepath.Join(home, ".tokenops", "routing-approvals.jsonl"), nil
}

// Propose records that a route was refused pending an answer. Repeated
// proposals for the same route are appended rather than deduplicated;
// the fold counts them so callers can show how often it came up.
func (s *Store) Propose(r Record) error {
	r.Kind = KindProposed
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	return s.append(r)
}

// Decide records the operator's answer.
func (s *Store) Decide(key, decision, chosenModel string) error {
	return s.append(Record{
		Kind:        KindDecided,
		At:          time.Now().UTC(),
		Key:         key,
		Decision:    decision,
		ChosenModel: chosenModel,
	})
}

func (s *Store) append(r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("routingapproval: encode: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("routingapproval: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("routingapproval: write: %w", err)
	}
	return nil
}

// Load folds the log into current per-route state, newest decision
// winning. Malformed lines are skipped rather than failing the read — a
// truncated final write must not make the whole log unreadable.
func (s *Store) Load() (map[string]State, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]State{}, nil
		}
		return nil, fmt.Errorf("routingapproval: open: %w", err)
	}
	defer func() { _ = f.Close() }()

	out := map[string]State{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Key == "" {
			continue
		}
		cur := out[r.Key]
		switch r.Kind {
		case KindProposed:
			seen := cur.Seen + 1
			if !cur.Decided {
				cur.Record = r
			}
			cur.Seen = seen
		case KindDecided:
			cur.Decision = r.Decision
			cur.ChosenModel = r.ChosenModel
			cur.Key = r.Key
			cur.At = r.At
			cur.Decided = true
		default:
			continue
		}
		out[r.Key] = cur
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("routingapproval: read: %w", err)
	}
	return out, nil
}

// Pending returns undecided proposals, most recently seen first.
func (s *Store) Pending() ([]State, error) {
	all, err := s.Load()
	if err != nil {
		return nil, err
	}
	out := make([]State, 0, len(all))
	for _, st := range all {
		if !st.Decided {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

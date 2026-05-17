// Package tasks tracks operator-marked task boundaries so the
// scorecard, coach, and analytics layers can compute task-level
// metrics (cost-per-task, iteration depth, TTFUO).
//
// Markers live in a JSONL file at $HOME/.tokenops/tasks.jsonl so
// the operator can grep / diff their task history offline. No
// schema changes to events.db. The CLI commands `tokenops task
// start <description>` and `tokenops task done` append markers;
// `tokenops task list` reads them back.
package tasks

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Task is one operator-marked unit of work. StartedAt is set on
// the start marker; CompletedAt is set when the matching done
// marker is appended. ID is a short ULID-ish stamp the operator
// can reference in follow-up commands.
type Task struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	// SessionID, when set, ties the task to a Claude Code session.
	// Optional — the operator can run `tokenops task start` outside
	// any session (e.g. for a generic chore).
	SessionID string `json:"session_id,omitempty"`
}

// IsOpen reports whether the task has no completion marker yet.
func (t Task) IsOpen() bool {
	return t.CompletedAt.IsZero()
}

// Duration returns the wall-clock span of the task; for open tasks
// returns time since StartedAt against the supplied clock.
func (t Task) Duration(clock func() time.Time) time.Duration {
	if t.CompletedAt.IsZero() {
		return clock().Sub(t.StartedAt)
	}
	return t.CompletedAt.Sub(t.StartedAt)
}

// DefaultPath resolves to $HOME/.tokenops/tasks.jsonl. Returns
// empty string when the home dir lookup fails (caller decides
// how to surface that — usually as a soft warning).
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".tokenops", "tasks.jsonl")
}

// markerKind discriminates start vs done entries in the JSONL.
type markerKind string

const (
	markerStart markerKind = "start"
	markerDone  markerKind = "done"
)

// marker is the per-line shape written to tasks.jsonl. Two
// markers per task: one with Kind=start (carries Description +
// StartedAt), one with Kind=done (carries CompletedAt). Pairing
// is by ID.
type marker struct {
	Kind        markerKind `json:"kind"`
	ID          string     `json:"id"`
	Description string     `json:"description,omitempty"`
	SessionID   string     `json:"session_id,omitempty"`
	Timestamp   time.Time  `json:"timestamp"`
}

// Start appends a new task-start marker. ID is generated from the
// current timestamp; description is what the operator typed.
// SessionID is optional — empty when the operator isn't pinning
// the task to a Claude Code session.
func Start(path, description, sessionID string, clock func() time.Time) (Task, error) {
	if description == "" {
		return Task{}, errors.New("description required")
	}
	if clock == nil {
		clock = time.Now
	}
	now := clock().UTC()
	t := Task{
		ID:          generateID(now),
		Description: description,
		StartedAt:   now,
		SessionID:   sessionID,
	}
	if err := appendMarker(path, marker{
		Kind:        markerStart,
		ID:          t.ID,
		Description: description,
		SessionID:   sessionID,
		Timestamp:   now,
	}); err != nil {
		return Task{}, err
	}
	return t, nil
}

// Done closes the most recent open task. Returns the closed Task,
// or an error if there is no open task.
func Done(path string, clock func() time.Time) (Task, error) {
	if clock == nil {
		clock = time.Now
	}
	all, err := List(path)
	if err != nil {
		return Task{}, err
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].IsOpen() {
			now := clock().UTC()
			if err := appendMarker(path, marker{
				Kind:      markerDone,
				ID:        all[i].ID,
				Timestamp: now,
			}); err != nil {
				return Task{}, err
			}
			all[i].CompletedAt = now
			return all[i], nil
		}
	}
	return Task{}, errors.New("no open task — run `tokenops task start <description>` first")
}

// List returns every task in chronological order (oldest first).
// Open tasks have CompletedAt == zero.
func List(path string) ([]Task, error) {
	if path == "" {
		path = DefaultPath()
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	tasks := map[string]*Task{}
	var order []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		var m marker
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
		}
		switch m.Kind {
		case markerStart:
			if _, exists := tasks[m.ID]; exists {
				continue
			}
			t := &Task{
				ID:          m.ID,
				Description: m.Description,
				StartedAt:   m.Timestamp,
				SessionID:   m.SessionID,
			}
			tasks[m.ID] = t
			order = append(order, m.ID)
		case markerDone:
			if t, ok := tasks[m.ID]; ok {
				t.CompletedAt = m.Timestamp
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan tasks.jsonl: %w", err)
	}
	out := make([]Task, 0, len(order))
	for _, id := range order {
		out = append(out, *tasks[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out, nil
}

// appendMarker is the only writer to tasks.jsonl. Creates the
// parent directory on first use; opens append-only so concurrent
// invocations from multiple shells don't corrupt the file.
func appendMarker(path string, m marker) error {
	if path == "" {
		path = DefaultPath()
	}
	if path == "" {
		return errors.New("tasks: HOME unresolved; pass --path explicitly")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// generateID returns a short, sortable, human-readable task ID
// based on the timestamp. Format: 2026-05-17T13-45-30-abc where
// the suffix is the millisecond+random nonce.
func generateID(t time.Time) string {
	return strings.ReplaceAll(t.Format("20060102T150405.000"), ".", "-")
}

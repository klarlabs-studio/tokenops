// Package routehistory keeps the dated record of where each harness sent
// its requests (ADR 0009), as JSON lines under ~/.tokenops.
//
// Claude Code's transcripts name the model that answered, not the
// endpoint, so the daemon watches the setting and records each change.
// The first observation is dated from evidence when there is some: when
// FireConnect pointed Claude Code at Fireworks, the backup it took of the
// previous settings says when.
package routehistory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/biller"
)

// HarnessClaudeCode is the harness name Claude Code's routes are kept under.
const HarnessClaudeCode = "claude-code"

// Tracker is the route history, in memory and on disk.
type Tracker struct {
	path string
	home string

	mu     sync.RWMutex
	routes biller.Routes
}

// Open loads the history at ~/.tokenops/route-history.jsonl.
func Open() (*Tracker, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return OpenAt(filepath.Join(home, ".tokenops", "route-history.jsonl"), home)
}

// OpenAt loads the history at path; home is where evidence files are
// looked for.
func OpenAt(path, home string) (*Tracker, error) {
	t := &Tracker{path: path, home: home}
	data, err := os.ReadFile(path) //nolint:gosec // TokenOps' own history file
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var r biller.Route
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Harness != "" {
			t.routes = append(t.routes, r)
		}
	}
	return t, sc.Err()
}

// Routes is a copy of the history.
func (t *Tracker) Routes() biller.Routes {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append(biller.Routes(nil), t.routes...)
}

// At is the base URL harness pointed at at ts, "" when unknown.
func (t *Tracker) At(harness string, ts time.Time) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	u, _ := t.routes.At(harness, ts)
	return u
}

// Observe records harness pointing at baseURL now, when that differs from
// the latest route. It returns whether anything was written.
func (t *Tracker) Observe(harness, baseURL string, now time.Time) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	latest, known := t.routes.Latest(harness)
	if known && latest.BaseURL == baseURL {
		return false, nil
	}
	add := []biller.Route{{Harness: harness, BaseURL: baseURL, From: now, Recorded: now, Evidence: "observed"}}
	if !known {
		// The first sighting: date it from evidence when there is some,
		// with the default endpoint before it. Without evidence, the
		// first route applies back to the start, which is the best
		// knowledge there is.
		if since, evidence, ok := t.datedSwitch(harness, baseURL); ok && since.Before(now) {
			add = []biller.Route{
				{Harness: harness, From: time.Time{}, Recorded: now, Evidence: "before " + evidence},
				{Harness: harness, BaseURL: baseURL, From: since, Recorded: now, Evidence: evidence},
			}
		}
	}
	if err := t.append(add); err != nil {
		return false, err
	}
	t.routes = append(t.routes, add...)
	return true, nil
}

// datedSwitch finds when harness was pointed at baseURL from a file the
// tool that switched it left behind.
func (t *Tracker) datedSwitch(harness, baseURL string) (time.Time, string, bool) {
	if harness != HarnessClaudeCode || t.home == "" {
		return time.Time{}, "", false
	}
	if e, ok := biller.EndpointFor(baseURL); !ok || e.Provider != "fireworks" {
		return time.Time{}, "", false
	}
	// FireConnect snapshots Claude Code's settings the first time it
	// routes them through Fireworks, and keeps the snapshot until
	// `fireconnect claude off`.
	st, err := os.Stat(filepath.Join(t.home, ".fireconnect", "claude", "provider-backup.json"))
	if err != nil {
		return time.Time{}, "", false
	}
	return st.ModTime().UTC(), "fireconnect backup", true
}

func (t *Tracker) append(rs []biller.Route) error {
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(t.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // TokenOps' own history file
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for _, r := range rs {
		line, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

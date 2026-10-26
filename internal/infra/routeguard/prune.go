package routeguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The guard keeps two files per session in its directory, and nothing
// else: session-<id>.json (the kinds argued, advice awaiting its outcome,
// subagent moves put to the operator) and headroom-<id>.json (the steps
// the headroom note last named). Both matter only while the session runs;
// the follow-through and savings ledgers live elsewhere. Without pruning
// the directory gained two files for every session ever run.
const (
	// sessionIdle is how long a session's files go unwritten before they
	// are removed. A session resumed after that argues a kind once more
	// and repeats the headroom note: harmless.
	sessionIdle = 24 * time.Hour
	pruneEvery  = time.Hour
	pruneStamp  = ".pruned"
)

// perSessionPrefixes are the per-session files PruneSessions may remove.
var perSessionPrefixes = []string{"session-", "headroom-"}

// PruneSessions removes per-session files not written for a day, unless it
// last ran within the hour (a stamp file's mtime records when). Any other
// file in dir is left alone, and a file it cannot remove is left for the
// next run.
func PruneSessions(dir string, now time.Time) error {
	if dir == "" {
		return nil
	}
	stamp := filepath.Join(dir, pruneStamp)
	if st, err := os.Stat(stamp); err == nil && now.Sub(st.ModTime()) < pruneEvery {
		return nil
	} else if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if err := os.WriteFile(stamp, nil, 0o600); err != nil {
		return err
	}
	if err := os.Chtimes(stamp, now, now); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !perSessionFile(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < sessionIdle {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	return nil
}

func perSessionFile(name string) bool {
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	for _, p := range perSessionPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

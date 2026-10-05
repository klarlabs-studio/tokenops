package findings

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// The session analysis reads every transcript of the last week, which
// takes minutes on a busy machine, so it does not run per glance: the
// daemon runs it in the background and keeps the answer here, and every
// surface reads that.

// SessionsSnapshot is the session analysis as of when it ran.
type SessionsSnapshot struct {
	ComputedAt time.Time   `json:"computed_at"`
	DX         sessions.DX `json:"dx"`
	// Curve is how first-try success changes as context grows.
	Curve []agentdx.ContextBand `json:"context_curve,omitempty"`
}

// MaxSnapshotAge is how old an analysis `tokenops dx` still answers from;
// the daemon refreshes it every three hours.
const MaxSnapshotAge = 6 * time.Hour

// snapshotFile names the cached analysis under the TokenOps directory.
const snapshotFile = "session-findings.json"

// DefaultDir is ~/.tokenops/cache.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".tokenops", "cache")
	}
	return filepath.Join(home, ".tokenops", "cache")
}

// ReadSnapshot reads the last analysis; nil when none has run.
func ReadSnapshot(dir string) (*SessionsSnapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, snapshotFile)) //nolint:gosec // TokenOps' own cache directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s SessionsSnapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// WriteSnapshot stores s, replacing the file in one rename so a reader
// never sees half of it.
func WriteSnapshot(dir string, s SessionsSnapshot) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, snapshotFile+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, snapshotFile))
}

// AnalyzeSessions runs the week's session analysis and stores it. The
// analysis reads transcripts but stores only derived figures.
func AnalyzeSessions(ctx context.Context, dir string, now time.Time) (SessionsSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return SessionsSnapshot{}, err
	}
	dx, curve := sessions.ComputeDXWithCurve(sessions.Window{Days: 7}, now)
	s := SessionsSnapshot{ComputedAt: now.UTC(), DX: dx, Curve: curve}
	return s, WriteSnapshot(dir, s)
}

// Gather reads the coach's ledgers and the last session analysis around
// a glance and the coach's report, either of which may be nil. A ledger
// that cannot be read leaves its findings out.
func Gather(g *headroom.Glance, c *coach.Report, cacheDir string) Inputs {
	in := Inputs{Glance: g, Coach: c}
	if s, err := coachhook.ReadStats(""); err == nil && s.Events > 0 {
		in.CoachHook = &s
	}
	if s, err := readguard.ReadStats(""); err == nil && s.Events > 0 {
		in.ReadGuard = &s
	}
	if s, err := ReadSnapshot(cacheDir); err == nil {
		in.Sessions = s
	}
	return in
}

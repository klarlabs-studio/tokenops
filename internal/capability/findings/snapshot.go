package findings

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/prefixlint"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/jsonfinite"
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
//
// encoding/json refuses NaN and ±Inf and fails the whole document over
// one of them, so a figure that is not a number is stored as 0 and its
// JSON path returned in dropped, for the caller to name. One bad figure
// used to cost the entire analysis.
func WriteSnapshot(dir string, s SessionsSnapshot) (dropped []string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dropped = jsonfinite.Sanitize(&s)
	b, err := json.Marshal(s)
	if err != nil {
		return dropped, err
	}
	tmp, err := os.CreateTemp(dir, snapshotFile+".*")
	if err != nil {
		return dropped, err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return dropped, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return dropped, err
	}
	return dropped, os.Rename(tmp.Name(), filepath.Join(dir, snapshotFile))
}

// AnalyzeSessions runs the week's session analysis and stores it. The
// analysis reads transcripts but stores only derived figures. A non-nil
// cache, kept between runs, makes each run read only the transcripts
// that changed since the last. dropped names each figure that was not a
// number and was stored as 0 (see WriteSnapshot).
func AnalyzeSessions(ctx context.Context, dir string, now time.Time, cache *sessions.TranscriptCache) (s SessionsSnapshot, dropped []string, err error) {
	if err := ctx.Err(); err != nil {
		return SessionsSnapshot{}, nil, err
	}
	dx, curve := sessions.ComputeDXWithCurve(sessions.Window{Days: 7, Cache: cache}, now)
	s = SessionsSnapshot{ComputedAt: now.UTC(), DX: dx, Curve: curve}
	dropped, err = WriteSnapshot(dir, s)
	return s, dropped, err
}

// WindowDays is how far back the findings read the coach's ledgers, the
// same week the session analysis covers. The ledgers hold every event since
// install: read whole, a count from July sat beside this week's figures and
// read as this week's.
const WindowDays = 7

// ledgerStats reads the coach hook's and the read guard's ledgers from
// since on; an empty dir is the default location. A ledger that cannot be
// read, or holds nothing in the window, is nil.
func ledgerStats(hookDir, guardDir string, since time.Time) (*coachhook.Stats, *readguard.Stats) {
	var hook *coachhook.Stats
	var guard *readguard.Stats
	if s, err := coachhook.ReadStatsSince(hookDir, since); err == nil && s.Events > 0 {
		hook = &s
	}
	if s, err := readguard.ReadStatsSince(guardDir, since); err == nil && s.Events > 0 {
		guard = &s
	}
	return hook, guard
}

// Gather reads the coach's ledgers, the last session analysis, the
// volatile-prefix lint of the user's context files and the headroom pause
// around a glance, the coach's report and the config, any
// of which may be nil. A ledger that cannot be read leaves its findings
// out.
func Gather(g *headroom.Glance, c *coach.Report, cfg *config.Config, cacheDir string) Inputs {
	in := Inputs{Glance: g, Coach: c, Pause: pauseSummary(cfg), Loop: loopSummary(cfg), Presence: presenceStatus(cfg)}
	if cfg != nil {
		in.Extras = boundaries.ActiveExtras(boundaries.Env{})
	}
	in.CoachHook, in.ReadGuard = ledgerStats("", "", time.Now().AddDate(0, 0, -WindowDays))
	if s, err := ReadSnapshot(cacheDir); err == nil {
		in.Sessions = s
	}
	var ledger coach.Ledger
	if l, err := followthrough.Default(); err == nil {
		ledger = l
	}
	in.Anomalies = openAnomalies(cacheDir, ledger, time.Now())
	if s, err := avoidable.ReadSnapshot(cacheDir); err == nil {
		in.Waste = s
	}
	// The user's own context files and hooks: a few small files, no
	// transcripts, no project.
	lint := prefixlint.Lint(prefixlint.UserSources())
	in.Context = &lint
	return in
}

// DX answers w from the daemon's last analysis when w is the week it
// analyses and that analysis is under MaxSnapshotAge old; otherwise it
// reads the transcripts, which takes minutes on a busy machine.
func DX(w sessions.Window, now time.Time) sessions.DX {
	if w.Root == "" && !w.All && (w.Days == 0 || w.Days == 7) && !w.Filtered() {
		if s, err := ReadSnapshot(DefaultDir()); err == nil && s != nil &&
			now.Sub(s.ComputedAt) <= MaxSnapshotAge && s.DX.Metrics.Prompts > 0 {
			return s.DX
		}
	}
	return sessions.ComputeDX(w, now)
}

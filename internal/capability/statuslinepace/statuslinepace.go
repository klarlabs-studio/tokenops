// Package statuslinepace is the pace forecast the daemon hands the status
// line: when each plan window runs out at the rate glance forecasts, kept
// in one small file under ~/.tokenops. The status line runs on every turn
// and must never wait on the daemon, the event store or the network, so
// the daemon writes the forecast it has already computed and the status
// line reads it only while it is fresh and describes the window on the
// line.
//
// The file holds figures only: providers, window names, times and the
// words for the rate a forecast read. No prompt, session or account.
package statuslinepace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/tempfile"
)

// FileName is the file under ~/.tokenops.
const FileName = "statusline.json"

// FreshFor is how old a forecast the status line still shows. The daemon
// writes one every Every (five minutes), so ten minutes rides out a missed
// write; beyond it the daemon is likely stopped, and a run-out time from
// the last hour's rate would describe an hour that is no longer the last.
const FreshFor = 10 * time.Minute

// MaxWindows bounds the windows in the file, and MaxSize its bytes: a file
// larger than the daemon writes is not read.
const (
	MaxWindows = 16
	MaxSize    = 8 << 10
)

// futureSkew is how far ahead of the reader's clock a forecast may be
// computed: the daemon and the status line share one clock, so more says
// the file is not one to trust.
const futureSkew = time.Minute

// resetTolerance is how far apart two resets may be and name the same
// window: sources round a reset to the second or the minute, and the
// window after it resets five hours or a week later.
const resetTolerance = 2 * time.Minute

// Window is one plan window's forecast.
type Window struct {
	// Provider is the plan's provider ("anthropic", "openai").
	Provider string `json:"provider"`
	// Name is "5h" or "week".
	Name     string    `json:"name"`
	ResetsAt time.Time `json:"resets_at"`
	// RunsOutAt is when it runs out at the forecast's rate, before its
	// reset; zero when it lasts to its reset or its pace is not known.
	RunsOutAt time.Time `json:"runs_out_at,omitzero"`
	// Rate is the rate the forecast read, in words ("at the last hour's
	// rate"); empty with no RunsOutAt.
	Rate string `json:"rate,omitempty"`
}

// File is what the daemon writes.
type File struct {
	// ComputedAt is the now the forecasts were computed for.
	ComputedAt time.Time `json:"computed_at"`
	// Version is the daemon's, for tracing a file to what wrote it.
	Version string   `json:"version,omitempty"`
	Windows []Window `json:"windows,omitempty"`
}

// DefaultPath is where the file lives.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".tokenops", FileName)
	}
	return filepath.Join(home, ".tokenops", FileName)
}

// Build is the forecast of every plan's 5h and weekly window in reports,
// computed for at: the one forecast glance shows (VendorWindow.Forecast).
// A window that is not current, names no reset, or is another limit than
// the plan's own is left out; a provider's window is kept once.
func Build(reports []plans.HeadroomReport, at time.Time, version string) File {
	f := File{ComputedAt: at.UTC(), Version: version}
	seen := map[string]bool{}
	for _, r := range reports {
		for _, w := range r.Windows {
			if len(f.Windows) == MaxWindows {
				return f
			}
			key := r.Provider + "\x00" + w.Name
			if seen[key] || !paced(w, at) {
				continue
			}
			seen[key] = true
			out := Window{Provider: r.Provider, Name: w.Name, ResetsAt: w.ResetsAt.UTC()}
			if p := w.Forecast(at); p != nil && !p.TooEarly && !p.LastsToReset && p.RunsOutIn > 0 {
				if end := at.Add(p.RunsOutIn); end.Before(w.ResetsAt) {
					out.RunsOutAt, out.Rate = end.UTC(), plans.RateWords(p.Basis)
				}
			}
			f.Windows = append(f.Windows, out)
		}
	}
	return f
}

// paced reports whether w is a window the status line can show a
// forecast for at at: the plan's own 5h or weekly window, current, with
// a reset.
func paced(w plans.VendorWindow, at time.Time) bool {
	if w.Name != "5h" && w.Name != "week" {
		return false
	}
	return w.VendorLabel == "" && !w.NotStarted && !w.ResetsAt.IsZero() && w.Current(at)
}

// Write replaces the file at path with f, through a temporary file and a
// rename, so a reader never sees half of one. It is private to the
// operator (0600), and a file over MaxSize is refused.
func Write(path string, f File) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(b) > MaxSize {
		return fmt.Errorf("statusline pace: %d bytes, over %d", len(b), MaxSize)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := tempfile.Create(filepath.Dir(path), FileName+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Read returns the file at path; ok is false when it is missing,
// unreadable, malformed, oversized or names no time.
func Read(path string) (File, bool) {
	fh, err := os.Open(path) //nolint:gosec // the operator's own ~/.tokenops file
	if err != nil {
		return File{}, false
	}
	defer func() { _ = fh.Close() }()
	b, err := io.ReadAll(io.LimitReader(fh, MaxSize+1))
	if err != nil || len(b) > MaxSize {
		return File{}, false
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil || f.ComputedAt.IsZero() {
		return File{}, false
	}
	return f, true
}

// RunsOut is when provider's window name, resetting at resetsAt, runs out
// before its reset, and the rate that forecast read. ok is false when f
// is stale at now, the window is not in it or resets at another time (it
// is another window), it lasts to its reset, or its run-out time has
// passed.
func (f File) RunsOut(provider, name string, resetsAt, now time.Time) (time.Time, string, bool) {
	if f.ComputedAt.IsZero() || resetsAt.IsZero() || f.ComputedAt.After(now.Add(futureSkew)) || now.Sub(f.ComputedAt) > FreshFor {
		return time.Time{}, "", false
	}
	for _, w := range f.Windows {
		if w.Provider != provider || w.Name != name || w.ResetsAt.Sub(resetsAt).Abs() > resetTolerance {
			continue
		}
		if w.RunsOutAt.IsZero() || !w.RunsOutAt.After(now) || !w.RunsOutAt.Before(resetsAt) {
			return time.Time{}, "", false
		}
		return w.RunsOutAt, w.Rate, true
	}
	return time.Time{}, "", false
}

// Every is how often a Keeper writes the forecast: half of FreshFor, so one
// missed write still leaves the file fresh for the status line.
const Every = FreshFor / 2

// Source reads the plan headroom reports for now: the figures glance
// forecasts from.
type Source func(ctx context.Context, now time.Time) ([]plans.HeadroomReport, error)

// Keeper writes the forecast on its own schedule, so the status line has
// one while no client asks the daemon for the glance.
type Keeper struct {
	Path, Version string
	Source        Source
	Now           func() time.Time
	// Warn is told once when writes start failing, and again after they
	// recover and fail anew. A cancelled run is not a failure.
	Warn func(error)
}

// Run writes the forecast now and then at each tick, until ctx is done.
func (k Keeper) Run(ctx context.Context, tick <-chan time.Time) {
	failing := false
	for {
		err := k.Once(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil && !failing:
			failing = true
			k.Warn(err)
		case err == nil:
			failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
	}
}

// Once reads the forecast for now and writes it to Path.
func (k Keeper) Once(ctx context.Context) error {
	now := k.Now().UTC()
	reports, err := k.Source(ctx, now)
	if err != nil {
		return fmt.Errorf("statusline forecast: %w", err)
	}
	return Write(k.Path, Build(reports, now, k.Version))
}

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/checkup"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/planoverlay"
	"go.klarlabs.de/tokenops/internal/capability/ratecards"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/version"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The overlay of observed plan limits reads the window readings and
// requests of the last 90 days. It follows its inputs (ADR 0019), in
// three tiers:
//
//   - Every observedCheckEvery it reads the event store's position, which
//     is two numbers. Unchanged, nothing else runs.
//   - When the position moved, it reads again only the UTC hours the change
//     names (observed_days.go), and keeps the overlay from the hours.
//   - Once a day it checks a digest of the events the change log does not
//     hold (older than its horizon), and every observedMaxAge it reads
//     the whole window again.
//
// The mark of the last result is kept on disk, so a restart resumes from
// it: the first pass after a start is the delay's, whatever the mark says.
// A change to the config, the plan history, the rate cards or the binary
// (its plan catalog) is computed again from scratch.
const (
	observedDelay      = 20 * time.Minute
	observedCheckEvery = time.Hour
	// observedEvery is how often the digest is checked, and how often the
	// window slides on the clock alone when nothing moved.
	observedEvery = 24 * time.Hour
	// observedMaxAge is the oldest a result is kept without a rebuild.
	observedMaxAge = 7 * 24 * time.Hour
	// observedVersion is the computation's version: a change to how the
	// overlay is computed or what it keeps bumps it, and the next pass
	// computes it again. Version 2 keeps the inputs by hour.
	observedVersion = 2
)

// observedMarkFile names the mark under the cache directory.
const observedMarkFile = "observed-limits-inputs.json"

// observedDaysDir names the hours' inputs under the cache directory.
const observedDaysDir = "observed-days"

// observedJob is the overlay's computation and where its inputs stand.
type observedJob struct {
	options func(time.Time) checkup.Options
	// settings is a key of every input but the event log: the config, the
	// plan history, the rate cards, the build.
	settings func() string
	// store is the event log the overlay reads; nil reads the whole window
	// each time through the reference computation.
	store *sqlite.Store
	// hours is the window's inputs by hour, kept between passes.
	hours *checkup.DriftHours
	// dir holds the mark of the last result, and the hours' inputs.
	dir string
}

// observedMark is the inputs the last kept overlay reflects.
type observedMark struct {
	Version  int       `json:"version"`
	At       time.Time `json:"at"`      // when the overlay was last kept
	Built    time.Time `json:"built"`   // when the window was last read from scratch
	Checked  time.Time `json:"checked"` // when the digest last agreed
	Settings string    `json:"settings"`
	// Events is the store's position, "rowid.seq", read before the result
	// was computed: an event stored while it runs moves it past this mark.
	Events string `json:"events"`
	// Digest is the store's SpanDigest of the events older than the change
	// log's horizon as they stood at Built.
	Digest string `json:"digest"`
}

func startObservedRuntime(cfg config.Config, c *bootstrap.Components, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if c == nil || c.Store == nil || c.Spend == nil || len(cfg.Plans) == 0 {
		return
	}
	job := observedJob{
		options: func(now time.Time) checkup.Options {
			o := checkup.Options{Now: now, Events: c.Store, Plans: cfg.Plans, Price: c.Spend.ComputeAt}
			if h, err := planhistory.LoadDefault(); err == nil {
				o.PlanHistory = h
			}
			return o
		},
		settings: observedSettings(cfg),
		store:    c.Store,
		hours:    &checkup.DriftHours{Dir: filepath.Join(findings.DefaultDir(), observedDaysDir), Version: observedVersion},
		dir:      findings.DefaultDir(),
	}
	sup.Go("observed-limits", func(ctx context.Context) error {
		runObserved(ctx, job, observedDelay, observedCheckEvery, logger)
		return nil
	})
}

// observedSettings keys the inputs of the overlay other than events.
func observedSettings(cfg config.Config) func() string {
	key := configKey(cfg)
	var files []string
	if f, err := planhistory.Default(); err == nil {
		files = append(files, f.Path)
	}
	files = append(files, ratecards.SnapshotsDir(""))
	if cfg.Pricing.Path != "" {
		files = append(files, cfg.Pricing.Path)
	}
	return func() string {
		var b strings.Builder
		fmt.Fprintf(&b, "%d|%s|%s", observedVersion, version.Version, key)
		writeStamps(&b, files)
		return b.String()
	}
}

// position is the store's position as a mark's key.
func (j observedJob) position(ctx context.Context) (checkup.Position, error) {
	if j.store == nil {
		return checkup.Position{}, nil
	}
	p, err := j.store.Position(ctx)
	return checkup.Position{Rowid: p.Rowid, Seq: p.Seq}, err
}

// eventLog is the store as the overlay's inputs read it; nil without one.
func (j observedJob) eventLog() checkup.EventLog {
	if j.store == nil {
		return nil
	}
	return observedLog{store: j.store}
}

// observedLog adapts the store to the inputs' event log.
type observedLog struct{ store *sqlite.Store }

func (l observedLog) Position(ctx context.Context) (checkup.Position, error) {
	p, err := l.store.Position(ctx)
	return checkup.Position{Rowid: p.Rowid, Seq: p.Seq}, err
}

func (l observedLog) ChangedTimes(ctx context.Context, t eventschema.EventType, from, to checkup.Position) ([]time.Time, bool, error) {
	return l.store.ChangedTimes(ctx, t, sqlite.Position{Rowid: from.Rowid, Seq: from.Seq}, sqlite.Position{Rowid: to.Rowid, Seq: to.Seq})
}

func (l observedLog) EachEventBetween(ctx context.Context, t eventschema.EventType, from, until time.Time, fn func(*eventschema.Envelope) error) error {
	return l.store.EachEventBetween(ctx, t, from, until, fn)
}

// digest is the store's digest of the events older than the change log's
// horizon, as a result computed at built saw them.
func (j observedJob) digest(ctx context.Context, built time.Time) (string, error) {
	if j.store == nil {
		return "", nil
	}
	return j.store.SpanDigest(ctx, eventschema.EventTypePrompt,
		built.AddDate(0, 0, -checkup.DriftDays), built.Add(observedMaxAge-sqlite.ChangeLogHorizon))
}

// runObserved keeps the overlay from the tier comparisons, checking its
// inputs every every, the first check after delay.
func runObserved(ctx context.Context, job observedJob, delay, every time.Duration, logger *slog.Logger) {
	mark := readObservedMark(job.dir)
	wait := time.NewTimer(delay)
	defer wait.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wait.C:
		}
		mark, _ = keepObserved(ctx, job, mark, time.Now(), logger)
		wait.Reset(every)
	}
}

// keepObserved keeps the overlay at now unless mark's inputs still stand,
// and returns the mark of the result in force. When it computes, it also
// returns the drift comparisons it kept; nil when it kept the last one.
func keepObserved(ctx context.Context, job observedJob, mark *observedMark, now time.Time, logger *slog.Logger) (*observedMark, checkup.TierDrifts) {
	settings := job.settings()
	pos, err := job.position(ctx)
	if err != nil {
		logger.Warn("observed limits: store position unknown", "err", err)
		return mark, nil
	}
	key := fmt.Sprintf("%d.%d", pos.Rowid, pos.Seq)
	if mark != nil && mark.Settings == settings && mark.Events == key && fresh(mark, now) {
		logger.Debug("observed limits unchanged", "computed", mark.At)
		return mark, nil
	}
	full := mark == nil || mark.Settings != settings || now.Before(mark.Built) || now.Sub(mark.Built) >= observedMaxAge
	next := &observedMark{Version: observedVersion, At: now, Built: now, Checked: now, Settings: settings, Events: key}
	if !full && mark != nil {
		next.Built, next.Digest, next.Checked = mark.Built, mark.Digest, mark.Checked
		if now.Sub(mark.Checked) >= observedEvery {
			d, err := job.digest(ctx, mark.Built)
			if err != nil {
				logger.Warn("observed limits: digest not read", "err", err)
				return mark, nil
			}
			if d != mark.Digest {
				full = true
			} else {
				next.Checked = now
			}
		}
	}
	if full {
		d, err := job.digest(ctx, now)
		if err != nil {
			logger.Warn("observed limits: digest not read", "err", err)
			return mark, nil
		}
		next.Built, next.Checked, next.Digest = now, now, d
	}
	drifts, err := job.hours.Drifts(ctx, job.eventLog(), job.options(now), settings, pos, full)
	if err != nil {
		logger.Warn("observed limits not kept", "err", err)
		return mark, nil
	}
	if drifts == nil {
		return mark, nil
	}
	v, err := planoverlay.Keep(drifts, now)
	if err != nil {
		logger.Warn("observed limits not kept", "err", err)
		return mark, nil
	}
	logger.Debug("observed limits kept", "limits", len(v.Limits), "off", v.Off, "full", full)
	if err := writeObservedMark(job.dir, *next); err != nil {
		logger.Warn("observed limits' inputs not marked; the next start computes them again", "err", err)
	}
	return next, drifts
}

// fresh is whether a result computed at mark.At still stands at now with
// nothing to check: the inputs unchanged, the digest checked within a
// day, the window read within its max age.
func fresh(mark *observedMark, now time.Time) bool {
	return !now.Before(mark.At) && now.Sub(mark.At) < observedEvery &&
		now.Sub(mark.Checked) < observedEvery && now.Sub(mark.Built) < observedMaxAge
}

// readObservedMark is the stored mark, nil when there is none, it cannot
// be read, or it is another version's.
func readObservedMark(dir string) *observedMark {
	b, err := os.ReadFile(filepath.Join(dir, observedMarkFile)) //nolint:gosec // TokenOps' own cache directory
	if err != nil {
		return nil
	}
	var m observedMark
	if json.Unmarshal(b, &m) != nil || m.Version != observedVersion {
		return nil
	}
	return &m
}

// writeObservedMark stores m in one rename, so a reader never sees half.
func writeObservedMark(dir string, m observedMark) error {
	return checkup.WriteFileAtomic(filepath.Join(dir, observedMarkFile), m)
}

// writeStamps appends the size and modification time of each of files to
// b, "-" for one that is not there.
func writeStamps(b *strings.Builder, files []string) {
	for _, f := range files {
		st, err := os.Stat(f)
		switch {
		case err == nil:
			fmt.Fprintf(b, "|%d.%d", st.Size(), st.ModTime().UnixNano())
		case errors.Is(err, fs.ErrNotExist):
			b.WriteString("|-")
		default:
			b.WriteString("|?")
		}
	}
}

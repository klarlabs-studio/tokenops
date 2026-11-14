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
	"go.klarlabs.de/tokenops/internal/tempfile"
	"go.klarlabs.de/tokenops/internal/version"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The overlay of observed plan limits reads 90 days of window readings
// and requests, so it is kept in the background: a while after start and
// once a day, like the week's waste report. Tier ratios move over weeks.
//
// It follows its inputs (ADR 0019). The last result is marked with the
// inputs it reflects, on disk, so a restart does not compute it again: the
// next computation is due a day after the last one, as if the daemon had
// kept running, and is skipped when nothing it reads moved. A change to
// the config, the plan history, the rate cards or the binary (its plan
// catalog) is computed again after the start delay.
//
// Reading the 90 days was most of the daemon's CPU in the twenty minutes
// after every start, and most of its garbage collection.
const (
	observedDelay = 20 * time.Minute
	observedEvery = 24 * time.Hour
	// observedMaxAge is the oldest result kept while nothing moved: the
	// 90 days slide on with the clock alone.
	observedMaxAge = 7 * 24 * time.Hour
	// observedVersion is the computation's version: a change to how the
	// overlay is computed bumps it, and the next pass computes it again.
	observedVersion = 1
)

// observedMarkFile names the mark under the cache directory.
const observedMarkFile = "observed-limits-inputs.json"

// observedJob is the overlay's computation and where its inputs stand.
type observedJob struct {
	options func(time.Time) checkup.Options
	// settings is a key of every input but the event log: the config, the
	// plan history, the rate cards, the build.
	settings func() string
	// events is where the events a result computed at at reads stand;
	// ok is false when that cannot be told.
	events func(ctx context.Context, at time.Time) (string, bool)
	// dir holds the mark of the last result.
	dir string
}

// observedMark is the inputs the last kept overlay reflects.
type observedMark struct {
	Version  int       `json:"version"`
	At       time.Time `json:"at"`
	Settings string    `json:"settings"`
	Events   string    `json:"events"`
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
		events:   observedEvents(c.Store),
		dir:      findings.DefaultDir(),
	}
	sup.Go("observed-limits", func(ctx context.Context) error {
		runObserved(ctx, job, observedDelay, observedEvery, logger)
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

// observedEvents is where the events a result computed at at reads stand:
// the store's position, which an update or delete moves only for events
// younger than the change log's horizon, and a digest of the older part
// of the read, which every change moves. The digested span reaches to the
// horizon as it stands observedMaxAge later, past which the result is
// computed again anyway.
func observedEvents(store *sqlite.Store) func(context.Context, time.Time) (string, bool) {
	return func(ctx context.Context, at time.Time) (string, bool) {
		pos, err := store.Position(ctx)
		if err != nil {
			return "", false
		}
		since := at.AddDate(0, 0, -checkup.DriftDays)
		digest, err := store.SpanDigest(ctx, eventschema.EventTypePrompt, since, at.Add(observedMaxAge-sqlite.ChangeLogHorizon))
		if err != nil {
			return "", false
		}
		return fmt.Sprintf("%d|%d|%s", pos.Rowid, pos.Seq, digest), true
	}
}

// runObserved keeps the overlay from the tier comparisons, computing it
// when its inputs moved and at most once per every.
func runObserved(ctx context.Context, job observedJob, delay, every time.Duration, logger *slog.Logger) {
	mark := readObservedMark(job.dir)
	wait := time.NewTimer(observedFirstWait(mark, job.settings(), delay, every, time.Now()))
	defer wait.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wait.C:
		}
		mark = keepObserved(ctx, job, mark, time.Now(), logger)
		wait.Reset(every)
	}
}

// observedFirstWait is how long after a start at now the first pass
// waits: delay, or until the last result is due when it was computed
// under settings, a restart or not.
func observedFirstWait(mark *observedMark, settings string, delay, every time.Duration, now time.Time) time.Duration {
	if mark == nil || mark.Settings != settings {
		return delay
	}
	return max(delay, mark.At.Add(every).Sub(now))
}

// keepObserved computes and keeps the overlay at now unless mark's inputs
// still stand, and returns the mark of the result in force.
func keepObserved(ctx context.Context, job observedJob, mark *observedMark, now time.Time, logger *slog.Logger) *observedMark {
	settings := job.settings()
	if mark != nil && mark.Settings == settings && !now.Before(mark.At) && now.Sub(mark.At) < observedMaxAge {
		if events, ok := job.events(ctx, mark.At); ok && events == mark.Events {
			logger.Debug("observed limits unchanged", "computed", mark.At)
			return mark
		}
	}
	// Read before the computation: an event stored while it runs moves
	// the inputs past this mark, and the next pass computes again.
	events, known := job.events(ctx, now)
	drifts := checkup.ProviderDrifts(ctx, job.options(now))
	if drifts == nil {
		return mark
	}
	v, err := planoverlay.Keep(drifts, now)
	if err != nil {
		logger.Warn("observed limits not kept", "err", err)
		return mark
	}
	logger.Info("observed limits kept", "limits", len(v.Limits), "off", v.Off)
	if !known {
		return nil
	}
	next := &observedMark{Version: observedVersion, At: now, Settings: settings, Events: events}
	if err := writeObservedMark(job.dir, *next); err != nil {
		logger.Warn("observed limits' inputs not marked; the next start computes them again", "err", err)
	}
	return next
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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp, err := tempfile.Create(dir, observedMarkFile+".*")
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
	return os.Rename(tmp.Name(), filepath.Join(dir, observedMarkFile))
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

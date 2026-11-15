package checkup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/tempfile"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// TierDrifts is ProviderDrifts' result: the comparisons by provider.
type TierDrifts = map[string][]plans.TierDrift

// Position is where the event log stands: equal positions mean no event
// was added, updated or deleted between them (the store's own position).
type Position struct {
	Rowid int64 `json:"rowid"`
	Seq   int64 `json:"seq"`
	// Older counts the updates and deletes of events past the log's horizon,
	// which name no hour: a move of it reads every hour again.
	Older int64 `json:"older"`
}

// EventLog is the event store the hourly inputs are read from: the hour
// range reads and the change log of its events.
type EventLog interface {
	// Position is where the log stands now.
	Position(ctx context.Context) (Position, error)
	// ChangedTimes is the times of events of type t that changed between
	// from and to. attributable is false when a change cannot be placed in
	// time, and every hour must then be read again.
	ChangedTimes(ctx context.Context, t eventschema.EventType, from, to Position) (times []time.Time, attributable bool, err error)
	// EachEventBetween hands fn every event of type t in [from, until),
	// oldest first.
	EachEventBetween(ctx context.Context, t eventschema.EventType, from, until time.Time, fn func(*eventschema.Envelope) error) error
}

// hoursDirLayout names a day's file, UTC.
const hoursDirLayout = "2006-01-02"

// hoursIndexFile is the position the day files reflect, under the settings
// they were read with.
const hoursIndexFile = "index.json"

// hoursIndex is what hoursIndexFile holds.
type hoursIndex struct {
	Version  int      `json:"version"`
	Settings string   `json:"settings"`
	Position Position `json:"position"`
}

// hourInputs is one UTC hour's drift inputs, by provider.
type hourInputs struct {
	Readings map[string][]plans.QuotaReading `json:"readings,omitempty"`
	Turns    map[string][]plans.DriftTurn    `json:"turns,omitempty"`
}

// dayInputs is one UTC day's hours.
type dayInputs struct {
	Hours [24]hourInputs `json:"hours"`
}

// DriftHours keeps the drift comparisons' inputs by UTC hour (ADR 0019):
// each hour's window readings and plan-covered requests, priced, as the
// full read gives them, one file per day under Dir. A pass reads again
// only the hours the event log's change names, and assembles the window
// from the hours, which gives what the full read gives for the same events
// (the parity tests hold the two together). The decoded window is kept
// between passes, so a pass reads the disk only after a start.
//
// A late event lands in its own hour. An event the log cannot place (a
// recent delete, whose time is gone) takes every hour as changed.
type DriftHours struct {
	// Dir holds the day files and the index.
	Dir string
	// Version is the layout's version: an index of another one is not read.
	Version int

	settings string
	pos      Position
	loaded   bool
	days     map[time.Time]*dayInputs
	read     map[time.Time]bool
}

// Read is the hours the last Drifts read from the log, for tests.
func (h *DriftHours) Read() map[time.Time]bool {
	out := make(map[time.Time]bool, len(h.read))
	for k := range h.read {
		out[k] = true
	}
	return out
}

// Drifts is the drift comparisons at o.Now from the hours. full reads every
// hour again; otherwise only the hours the log's change since the last pass
// names, and any hour with no stored input. settings keys the inputs other
// than events; pos is the log's position, read before this call. A nil log
// is the reference read, ProviderDrifts.
func (h *DriftHours) Drifts(ctx context.Context, log EventLog, o Options, settings string, pos Position, full bool) (TierDrifts, error) {
	if log == nil {
		return ProviderDrifts(ctx, o), nil
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	now := o.Now
	since := now.AddDate(0, 0, -DriftDays)
	hours := hourRange(since, now)
	idx, indexed := h.readIndex(settings)
	switch {
	case full || !indexed:
		h.days, h.loaded = map[time.Time]*dayInputs{}, false
	case !h.loaded || h.settings != settings || h.pos != idx.Position:
		// After a start, or when the held hours are not what the disk
		// holds: the day files are the window as the last pass left it.
		h.days = map[time.Time]*dayInputs{}
		h.loadDays(hours)
		h.settings, h.pos, h.loaded = settings, idx.Position, true
	}
	dirty, err := h.dirtyHours(ctx, log, idx, hours, pos, full)
	if err != nil {
		return nil, err
	}
	h.read = dirty
	changed := map[time.Time]bool{}
	for hr := range dirty {
		d := dayOf(hr)
		if h.days[d] == nil {
			h.days[d] = &dayInputs{}
		}
		in, err := readHour(ctx, log, o, hr)
		if err != nil {
			return nil, err
		}
		h.days[d].Hours[hr.Hour()] = in
		changed[d] = true
	}
	days := dayRange(since, now)
	for _, d := range days {
		if h.days[d] == nil {
			return nil, fmt.Errorf("drift inputs: day %s has no hours", d.Format(hoursDirLayout))
		}
	}
	for d := range changed {
		if err := WriteFileAtomic(h.dayPath(d), h.days[d]); err != nil {
			return nil, err
		}
	}
	if err := h.pruneDays(days); err != nil {
		return nil, err
	}
	if err := WriteFileAtomic(filepath.Join(h.Dir, hoursIndexFile), hoursIndex{
		Version: h.Version, Settings: settings, Position: pos,
	}); err != nil {
		return nil, err
	}
	h.settings, h.pos, h.loaded = settings, pos, true
	for d := range h.days {
		if d.Before(dayOf(since)) {
			delete(h.days, d)
		}
	}
	readings, turns := h.assemble(o.Plans, hours, since, now)
	return ProviderDriftsOf(o, since, readings, turns), nil
}

// readIndex is the stored index when it was written under settings by this
// version.
func (h *DriftHours) readIndex(settings string) (hoursIndex, bool) {
	var idx hoursIndex
	b, err := os.ReadFile(filepath.Join(h.Dir, hoursIndexFile)) //nolint:gosec // TokenOps' own cache directory
	if err != nil || json.Unmarshal(b, &idx) != nil {
		return idx, false
	}
	return idx, idx.Version == h.Version && idx.Settings == settings
}

// loadDays reads the day files of hours from disk; a day with no readable
// file stays absent, and its hours are read again.
func (h *DriftHours) loadDays(hours []time.Time) {
	for _, d := range dayRange(hours[0], hours[len(hours)-1]) {
		b, err := os.ReadFile(h.dayPath(d)) //nolint:gosec // TokenOps' own cache directory
		if err != nil {
			continue
		}
		var f dayInputs
		if json.Unmarshal(b, &f) == nil {
			h.days[d] = &f
		}
	}
}

// dirtyHours is the hours whose inputs must be read again: every one when
// there is no stored position to compare with or the change cannot be
// placed; otherwise the hours the change names and those of any day the
// window does not hold.
func (h *DriftHours) dirtyHours(ctx context.Context, log EventLog, idx hoursIndex, hours []time.Time, pos Position, full bool) (map[time.Time]bool, error) {
	if full || !h.loaded || pos.Older != idx.Position.Older {
		return allHours(hours), nil
	}
	times, attributable, err := log.ChangedTimes(ctx, eventschema.EventTypePrompt, idx.Position, pos)
	if err != nil {
		return nil, err
	}
	if !attributable {
		return allHours(hours), nil
	}
	dirty := map[time.Time]bool{}
	for _, t := range times {
		dirty[hourOf(t)] = true
	}
	for _, hr := range hours {
		if h.days[dayOf(hr)] == nil {
			dirty[hr] = true
		}
	}
	return dirty, nil
}

// readHour reads one UTC hour's events the way the full read does, per
// provider in o's plans.
func readHour(ctx context.Context, log EventLog, o Options, hour time.Time) (hourInputs, error) {
	end := hour.Add(time.Hour)
	in := hourInputs{Readings: map[string][]plans.QuotaReading{}, Turns: map[string][]plans.DriftTurn{}}
	one := make([]*eventschema.Envelope, 1)
	err := log.EachEventBetween(ctx, eventschema.EventTypePrompt, hour, end, func(e *eventschema.Envelope) error {
		one[0] = e
		for p := range o.Plans {
			in.Readings[p] = append(in.Readings[p], plans.QuotaReadingsFrom(one, p, hour, end)...)
			in.Turns[p] = append(in.Turns[p], plans.DriftTurnsFrom(one, p, hour, end, o.Price)...)
		}
		return nil
	})
	if err != nil {
		return hourInputs{}, fmt.Errorf("drift inputs hour %s: %w", hour.Format(time.RFC3339), err)
	}
	return in, nil
}

// assemble is the window's readings and requests from the hours: each event
// the window holds, in the order the full read gives them.
func (h *DriftHours) assemble(providers map[string]string, hours []time.Time, since, now time.Time) (map[string][]plans.QuotaReading, map[string][]plans.DriftTurn) {
	readings := map[string][]plans.QuotaReading{}
	turns := map[string][]plans.DriftTurn{}
	in := func(t time.Time) bool { return !t.Before(since) && !t.After(now) }
	for _, hr := range hours {
		part := h.days[dayOf(hr)].Hours[hr.Hour()]
		for p := range providers {
			for _, r := range part.Readings[p] {
				if in(r.At) {
					readings[p] = append(readings[p], r)
				}
			}
			for _, t := range part.Turns[p] {
				if in(t.At) {
					turns[p] = append(turns[p], t)
				}
			}
		}
	}
	for p := range readings {
		rs := readings[p]
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
	}
	return readings, turns
}

// pruneDays removes the day files that left the window.
func (h *DriftHours) pruneDays(days []time.Time) error {
	keep := make(map[string]bool, len(days))
	for _, d := range days {
		keep[d.Format(hoursDirLayout)+".json"] = true
	}
	entries, err := os.ReadDir(h.Dir)
	if err != nil {
		return fmt.Errorf("drift inputs: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == hoursIndexFile || keep[name] || !strings.HasSuffix(name, ".json") {
			continue
		}
		if _, err := time.Parse(hoursDirLayout, strings.TrimSuffix(name, ".json")); err != nil {
			continue
		}
		if err := os.Remove(filepath.Join(h.Dir, name)); err != nil {
			return fmt.Errorf("drift inputs: %w", err)
		}
	}
	return nil
}

func (h *DriftHours) dayPath(d time.Time) string {
	return filepath.Join(h.Dir, d.Format(hoursDirLayout)+".json")
}

// hourRange is each UTC hour from the one holding since to the one holding
// now, both included; dayRange the days.
func hourRange(since, now time.Time) []time.Time {
	var hours []time.Time
	for hr := hourOf(since); !hr.After(hourOf(now)); hr = hr.Add(time.Hour) {
		hours = append(hours, hr)
	}
	return hours
}

func dayRange(since, now time.Time) []time.Time {
	var days []time.Time
	for d := dayOf(since); !d.After(dayOf(now)); d = d.Add(24 * time.Hour) {
		days = append(days, d)
	}
	return days
}

// dayOf is the UTC day an instant falls in; hourOf the UTC hour.
func dayOf(t time.Time) time.Time  { return t.UTC().Truncate(24 * time.Hour) }
func hourOf(t time.Time) time.Time { return t.UTC().Truncate(time.Hour) }

func allHours(hours []time.Time) map[time.Time]bool {
	out := make(map[time.Time]bool, len(hours))
	for _, hr := range hours {
		out[hr] = true
	}
	return out
}

// WriteFileAtomic stores v as JSON at path in one rename, so a reader never
// sees half of it.
func WriteFileAtomic(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := tempfile.Create(dir, filepath.Base(path)+".*")
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
	return os.Rename(tmp.Name(), path)
}

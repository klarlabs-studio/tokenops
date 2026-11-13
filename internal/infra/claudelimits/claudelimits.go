// Package claudelimits keeps the newest plan limits Claude Code reported to
// its status line.
//
// Claude Code hands every status line command its session JSON, and for a
// claude.ai subscriber that JSON carries the 5-hour and weekly windows;
// behind a Claude apps gateway with spend limits, the spend limit too
// (https://code.claude.com/docs/en/statusline). That is the vendor's own
// reading, on the machine, with no login of TokenOps' own. The status line
// cannot write to the event store — it runs on every turn and must stay
// cheap — so it leaves the reading here and the daemon ingests it.
package claudelimits

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/tempfile"
)

// Window is one limit as Claude Code reports it.
type Window struct {
	// UsedPct runs from 0 to 100, above 100 once a spend limit is exceeded.
	UsedPct float64 `json:"used_percentage"`
	// ResetsAt is Unix epoch seconds.
	ResetsAt int64 `json:"resets_at"`
	// UsedUSD, LimitUSD and Period come only with a gateway spend limit,
	// and even then may be absent: Claude Code fetches them separately.
	UsedUSD  *float64 `json:"used_usd,omitempty"`
	LimitUSD *float64 `json:"limit_usd,omitempty"`
	Period   string   `json:"period,omitempty"`
}

// Reading is the rate_limits object of one status line update.
type Reading struct {
	ObservedAt time.Time `json:"observed_at"`
	FiveHour   *Window   `json:"five_hour,omitempty"`
	SevenDay   *Window   `json:"seven_day,omitempty"`
	SpendLimit *Window   `json:"spend_limit,omitempty"`
	// Earlier are the readings before this one, oldest first, at least
	// keepEvery apart and none older than KeepFor: what a hook reads the
	// last hour's rate from (the run-out forecast) without the event
	// store. Their own Earlier is empty.
	Earlier []Reading `json:"earlier,omitempty"`
}

// KeepFor is how far back Earlier reaches: the burn-rate forecast reads
// the newest reading between two hours and one hour before the current
// one (plans.BurnLongLookback), with a few minutes to spare.
const KeepFor = 2*time.Hour + 5*time.Minute

// keepEvery thins Earlier: Claude Code updates the status line on every
// turn, and a reading a minute is plenty for an hour's rate.
const keepEvery = time.Minute

// Between is the newest of r and its earlier readings observed in [since,
// until], without its own earlier readings; false when none was.
func (r Reading) Between(since, until time.Time) (Reading, bool) {
	if !r.ObservedAt.Before(since) && !r.ObservedAt.After(until) {
		r.Earlier = nil
		return r, true
	}
	for i := len(r.Earlier) - 1; i >= 0; i-- {
		if e := r.Earlier[i]; !e.ObservedAt.Before(since) && !e.ObservedAt.After(until) {
			e.Earlier = nil
			return e, true
		}
	}
	return Reading{}, false
}

// withEarlier is r keeping prev and prev's earlier readings as its own:
// those within KeepFor of r, at least keepEvery apart.
func (r Reading) withEarlier(prev Reading) Reading {
	floor := r.ObservedAt.Add(-KeepFor)
	candidates := append(append([]Reading(nil), prev.Earlier...), prev)
	r.Earlier = nil
	for _, e := range candidates {
		e.Earlier = nil
		if e.ObservedAt.Before(floor) || !e.ObservedAt.Before(r.ObservedAt) {
			continue
		}
		if n := len(r.Earlier); n > 0 && e.ObservedAt.Sub(r.Earlier[n-1].ObservedAt) < keepEvery {
			continue
		}
		r.Earlier = append(r.Earlier, e)
	}
	return r
}

// Empty reports whether the reading carries no window.
func (r Reading) Empty() bool {
	return r.FiveHour == nil && r.SevenDay == nil && r.SpendLimit == nil
}

const fileName = "claude-limits.json"

// DefaultPath is where the reading lives.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".tokenops", fileName)
	}
	return filepath.Join(home, ".tokenops", fileName)
}

func resolve(path string) string {
	if path != "" {
		return path
	}
	return DefaultPath()
}

// Write replaces the stored reading, keeping the one it replaces among the
// earlier readings. It writes a temporary file and renames it, so the
// daemon never reads half a reading. An empty reading is not written: a
// turn before the first API response carries no rate_limits, and that says
// nothing about the limits. A reading no newer than the stored one keeps
// only the earlier readings that came before it.
func Write(path string, r Reading) error {
	if r.Empty() {
		return nil
	}
	path = resolve(path)
	r.Earlier = nil
	if prev, ok, err := Read(path); err == nil && ok {
		r = r.withEarlier(prev)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := tempfile.Create(filepath.Dir(path), fileName+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Read returns the stored reading; ok is false when there is none.
func Read(path string) (Reading, bool, error) {
	b, err := os.ReadFile(resolve(path))
	if errors.Is(err, os.ErrNotExist) {
		return Reading{}, false, nil
	}
	if err != nil {
		return Reading{}, false, err
	}
	var r Reading
	if err := json.Unmarshal(b, &r); err != nil {
		return Reading{}, false, err
	}
	if r.Empty() || r.ObservedAt.IsZero() {
		return Reading{}, false, nil
	}
	return r, true, nil
}

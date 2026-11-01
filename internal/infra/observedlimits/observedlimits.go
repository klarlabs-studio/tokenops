// Package observedlimits keeps the local overlay of plan limits observed
// from the operator's own window readings (plans.ObservedLimits) in
// ~/.tokenops/observed-limits.json. It is this machine's only: the
// shipped catalog changes by pull request (ADR 0002).
package observedlimits

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/jsonfile"
)

// File is the overlay's JSON file.
type File struct {
	Path string
}

// Default is the file under the operator's home directory.
func Default() (File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return File{}, err
	}
	return File{Path: filepath.Join(home, ".tokenops", "observed-limits.json")}, nil
}

// Load reads the overlay. A missing file is an empty overlay, on; an
// unreadable one is an error, so a caller never takes it for the catalog
// by accident and overwrites the operator's choice to turn it off.
func (f File) Load() (plans.ObservedLimits, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return plans.ObservedLimits{}, nil
	}
	if err != nil {
		return plans.ObservedLimits{}, err
	}
	var o plans.ObservedLimits
	if err := json.Unmarshal(b, &o); err != nil {
		return plans.ObservedLimits{}, err
	}
	return o, nil
}

// Save replaces the file atomically.
func (f File) Save(o plans.ObservedLimits) error {
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	return jsonfile.WriteAtomic(f.Path, append(b, '\n'), 0o600)
}

// Update replaces each provider's observed limits with what its tier
// comparisons now support, and saves the overlay. An overlay that is off
// stays off and empty.
func (f File) Update(byProvider map[string][]plans.TierDrift, now time.Time) (plans.ObservedLimits, error) {
	o, err := f.Load()
	if err != nil {
		return plans.ObservedLimits{}, err
	}
	for provider, ds := range byProvider {
		o.Replace(provider, plans.ObservedFromDrifts(ds, now), now)
	}
	return o, f.Save(o)
}

// Reset turns the overlay off and empties it: projections use the
// catalog only until it is turned on again.
func (f File) Reset(now time.Time) (plans.ObservedLimits, error) {
	o := plans.ObservedLimits{Off: true, UpdatedAt: now}
	return o, f.Save(o)
}

// TurnOn lets limits be observed into the overlay again.
func (f File) TurnOn(now time.Time) (plans.ObservedLimits, error) {
	o, err := f.Load()
	if err != nil {
		o = plans.ObservedLimits{}
	}
	o.Off, o.UpdatedAt = false, now
	return o, f.Save(o)
}

// Loader reads the default overlay for a long-running process; nil reads
// as the catalog only. A file that cannot be read is the catalog only.
func Loader() func() plans.ObservedLimits {
	return func() plans.ObservedLimits {
		f, err := Default()
		if err != nil {
			return plans.ObservedLimits{}
		}
		o, err := f.Load()
		if err != nil {
			return plans.ObservedLimits{}
		}
		return o
	}
}

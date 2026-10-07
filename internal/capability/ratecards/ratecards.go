// Package ratecards keeps the sourced, timestamped pricing snapshots the
// cost engine prices with (ADR 0002): fetching a new rate card, judging it
// against the last one, storing it, and reading any of them back.
//
// `tokenops pricing` and the daemon's scheduled refresh both work through
// it, so a refresh means the same thing whoever runs it.
package ratecards

import (
	"errors"
	"fmt"

	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/pricingsource"
)

// Snapshot is a rate card with its provenance.
type Snapshot = pricing.Snapshot

// Change is one model's difference between two snapshots.
type Change = pricing.Change

// Anomaly is a rate the consistency guard does not believe.
type Anomaly = pricing.Anomaly

// Source fetches a rate card.
type Source = pricing.Source

// ErrUnknownSource is returned for a source name that names none.
var ErrUnknownSource = errors.New("unknown pricing source")

// SourceNamed is the source a name selects: default (LiteLLM, then
// models.dev), litellm or models.dev. A URL stands in for the source's
// own endpoint.
func SourceNamed(name, url string) (Source, error) {
	src := pricingsource.ByName(name, url)
	if src == nil {
		return nil, fmt.Errorf("%w %q (known: default, litellm, models.dev)", ErrUnknownSource, name)
	}
	return src, nil
}

// Assessment is a freshly fetched card judged against the stored ones.
type Assessment struct {
	// Anomalies are the consistency guard's objections to the new card.
	Anomalies []Anomaly
	// Previous is the card it is compared with: the latest stored
	// snapshot, or the embedded baseline when PreviousStored is false.
	Previous       Snapshot
	PreviousStored bool
	// Changes are the new card's differences from Previous.
	Changes []Change
}

// Assess lints fetched and diffs it against the latest snapshot in dir.
func Assess(dir string, fetched Snapshot) Assessment {
	prev, stored := pricing.LatestSnapshot(dir)
	return Assessment{
		Anomalies:      pricing.Check(fetched),
		Previous:       prev,
		PreviousStored: stored,
		Changes:        pricing.Diff(prev, fetched),
	}
}

// Save stores s in dir and returns the file it wrote.
func Save(dir string, s Snapshot) (string, error) { return pricing.SaveSnapshot(dir, s) }

// Find reads the snapshot selector names in dir: latest, baseline, or a
// timestamp as `pricing show` prints it (a prefix such as a date works).
func Find(dir, selector string) (Snapshot, error) { return pricing.FindSnapshot(dir, selector) }

// Diff is how new differs from old, model by model.
func Diff(old, new Snapshot) []Change { return pricing.Diff(old, new) }

// Lint is the consistency guard's objections to s.
func Lint(s Snapshot) []Anomaly { return pricing.Check(s) }

// FormatChange renders c as one line, e.g. "~ anthropic/x cache_read 0.5 → 1.5 (+200%)".
func FormatChange(c Change) string { return pricing.FormatChange(c) }

// Pinned is the set of snapshot keys for verified catalog rows, which the
// cost engine prices at the baseline whatever a snapshot says.
func Pinned() map[string]bool { return pricing.PinnedSnapshotKeys() }

// Dir is the snapshot directory dir names, ~/.tokenops/pricing when empty.
func Dir(dir string) string { return pricing.ResolveDir(dir) }

// Tables is the effective-dated rate card in dir with the operator's
// negotiated rates at overridesPath layered over every period, so a
// refresh cannot drop them: they outrank every fetched card. An
// override file that cannot be read is left out, not fatal: the engine
// keeps pricing.
func Tables(dir, overridesPath string) []spend.DatedTable {
	overrides := spend.Table{}
	if overridesPath != "" {
		if ov, err := spend.LoadTableFile(overridesPath); err == nil {
			overrides = ov
		}
	}
	return pricing.EffectiveTables(dir, overrides)
}

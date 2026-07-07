package pricing

import (
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// AllSnapshots returns the embedded baseline followed by every persisted
// snapshot under dir. The baseline sorts first (its FetchedAt is fixed at
// the ADR's acceptance), so events predating any real refresh still price on
// the baseline. Fail-soft: a missing/unreadable dir yields just the baseline.
func AllSnapshots(dir string) []Snapshot {
	out := make([]Snapshot, 0, 4)
	out = append(out, BaselineSnapshot())
	out = append(out, LoadSnapshots(dir)...)
	return out
}

// SnapshotsToDatedTables converts each snapshot into a spend.DatedTable
// effective from its FetchedAt. A snapshot now spans every provider the catalog
// prices, but it may still be incomplete (a source can omit models), so each
// dated table layers the snapshot's rates onto a full spend.DefaultTable via
// MergeOverrides: the result is a COMPLETE rate card for that instant, and a
// snapshot rate overrides the matching Key{Provider, Model} row — so a fetched
// Mistral rate overrides the Mistral baseline, not just Anthropic. The baseline
// snapshot's rates equal the whole catalog, so its dated table is exactly
// DefaultTable() — a baseline-only engine reproduces current behavior.
func SnapshotsToDatedTables(snaps []Snapshot) []spend.DatedTable {
	out := make([]spend.DatedTable, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, spend.DatedTable{
			EffectiveFrom: s.FetchedAt,
			Table:         spend.DefaultTable().MergeOverrides(s.Table()),
		})
	}
	return out
}

// EffectiveEngine builds a spend.Engine that prices each event at the rate
// card in effect at the event's timestamp: the embedded baseline plus every
// persisted snapshot under dir, effective-dated by FetchedAt. When only the
// baseline exists this is equivalent to spend.NewEngine(spend.DefaultTable()).
// It never returns a nil engine and, being fail-soft on snapshot loading,
// never errors today; the error is retained for forward compatibility.
func EffectiveEngine(dir string) (*spend.Engine, error) {
	return EffectiveEngineWithOverrides(dir, spend.Table{})
}

// EffectiveEngineWithOverrides is EffectiveEngine with a user override table
// (negotiated rates, config: pricing.path) merged onto every dated table, so
// overrides remain honored across all effective periods. An empty overrides
// table is a no-op.
func EffectiveEngineWithOverrides(dir string, overrides spend.Table) (*spend.Engine, error) {
	dated := SnapshotsToDatedTables(AllSnapshots(dir))
	if len(overrides.Rates) > 0 {
		for i := range dated {
			dated[i].Table = dated[i].Table.MergeOverrides(overrides)
		}
	}
	return spend.NewDatedEngine(dated), nil
}

package coach

import (
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

// KindVolume is the tips of one kind in the look-back, and TipVolumeDays
// the look-back the coach's surfaces report.
type KindVolume = ft.KindVolume

// TipVolumeDays is the default look-back for TipVolume.
const TipVolumeDays = ft.VolumeDays

// TipVolume is the tips offered in the last days days by kind, with how
// many were seen to matter: the figures a kind's frequency is judged by.
// A ledger that cannot be read reports nothing.
func TipVolume(l Ledger, now time.Time, days int) []ft.KindVolume {
	if l == nil {
		return nil
	}
	entries, err := l.Load()
	if err != nil {
		return nil
	}
	return ft.Volume(ft.Fold(entries, now), now, days)
}

// MostlyUnseen returns whether tips of a kind went mostly unseen over the
// last week. The ledger is read on first use, since most turns ask
// nothing. A ledger that cannot be read answers no: missing evidence must
// not lower the coach's voice.
func MostlyUnseen(l Ledger, now time.Time) func(kind string) bool {
	var records []ft.Record
	loaded := false
	return func(kind string) bool {
		if l == nil {
			return false
		}
		if !loaded {
			loaded = true
			if entries, err := l.Load(); err == nil {
				records = ft.Fold(entries, now)
			}
		}
		return ft.MostlyUnseen(records, kind, now)
	}
}

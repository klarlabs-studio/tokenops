package coach

import (
	"errors"
	"fmt"
	"testing"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

func unseenTips(kind string, n int, now time.Time) []ft.Entry {
	out := make([]ft.Entry, 0, n)
	for i := range n {
		out = append(out, ft.Entry{
			Type: ft.EntryOffer, ID: fmt.Sprint(kind, i), At: now.Add(-time.Duration(30+i) * time.Hour),
			Power: "inform", Channel: ft.ChannelTip, Kind: kind, Via: ft.ViaStopHook,
		})
	}
	return out
}

func TestTipVolumeAndMostlyUnseenReadTheLedger(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	l := &memLedger{entries: unseenTips("quota_burn", 6, now)}

	vols := TipVolume(l, now, 7)
	if len(vols) != 1 || vols[0].Kind != "quota_burn" || vols[0].Unseen != 6 {
		t.Fatalf("volume = %+v", vols)
	}
	unseen := MostlyUnseen(l, now)
	if !unseen("quota_burn") || unseen("compact_now") {
		t.Error("mostly unseen should name the burn warning only")
	}
}

// Missing evidence must not lower the coach's voice.
func TestMostlyUnseenWithoutEvidenceIsNo(t *testing.T) {
	now := time.Now()
	if MostlyUnseen(nil, now)("quota_burn") || TipVolume(nil, now, 7) != nil {
		t.Error("nil ledger claimed evidence")
	}
	broken := &memLedger{loadErr: errors.New("disk")}
	if MostlyUnseen(broken, now)("quota_burn") || TipVolume(broken, now, 7) != nil {
		t.Error("unreadable ledger claimed evidence")
	}
}

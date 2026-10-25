package followthrough

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

var volNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// tipsOf builds n tip offers of kind, ago before volNow, none seen and
// none acted on, so they fold to unseen once their day is out.
func tipsOf(kind string, ago []time.Duration, seen bool) []Entry {
	var out []Entry
	for i, a := range ago {
		id := fmt.Sprintf("%s-%d-%d", kind, i, a)
		out = append(out, Entry{Type: EntryOffer, ID: id, At: volNow.Add(-a), Power: "inform", Channel: ChannelTip, Kind: kind, Via: ViaStopHook})
		if seen {
			out = append(out, Entry{Type: EntrySeen, ID: id, At: volNow.Add(-a), Via: ViaStatusLine})
		}
	}
	return out
}

func hours(hs ...int) []time.Duration {
	out := make([]time.Duration, len(hs))
	for i, h := range hs {
		out[i] = time.Duration(h) * time.Hour
	}
	return out
}

func TestVolumeCountsPerDayAndUnseenRate(t *testing.T) {
	var entries []Entry
	// quota_100: six tips over three days, none seen, all past their window.
	entries = append(entries, tipsOf("quota_100", hours(30, 31, 55, 56, 80, 81), false)...)
	// compact_now: two tips, both seen and acted on by a resolve.
	entries = append(entries, tipsOf("compact_now", hours(30, 100), true)...)
	for _, e := range entries {
		if e.Type == EntryOffer && e.Kind == "compact_now" {
			entries = append(entries, Entry{Type: EntryResolve, ID: e.ID, At: e.At.Add(time.Minute), Outcome: OutcomeFollowed})
		}
	}
	// A tip older than the window and an advice offer are not counted.
	entries = append(entries, tipsOf("quota_100", hours(24*9), false)...)
	entries = append(entries, Entry{Type: EntryOffer, ID: "adv", At: volNow.Add(-time.Hour), Channel: ChannelAdvice, Kind: "lookup"})
	// A fresh tip is still open.
	entries = append(entries, tipsOf("quota_100", hours(2), false)...)

	got := Volume(Fold(entries, volNow), volNow, 7)
	if len(got) != 2 || got[0].Kind != "quota_100" || got[1].Kind != "compact_now" {
		t.Fatalf("kinds = %+v", got)
	}
	q := got[0]
	if q.Offers != 7 || q.Open != 1 || q.Unseen != 6 {
		t.Errorf("quota_100 = %+v", q)
	}
	// Trailing 24h buckets, oldest first: 80/81h ago is day 3, 55/56h day 2,
	// 30/31h day 1, 2h ago day 0.
	if want := []int{0, 0, 0, 2, 2, 2, 1}; !reflect.DeepEqual(q.PerDay, want) {
		t.Errorf("per day = %v, want %v", q.PerDay, want)
	}
	if r, ok := q.UnseenRate(); !ok || r != 1 {
		t.Errorf("unseen rate = %v %v, want 1 of the 6 settled", r, ok)
	}
	if !q.MostlyUnseen() || !MostlyUnseen(Fold(entries, volNow), "quota_100", volNow) {
		t.Error("quota_100 should read as mostly unseen")
	}
	c := got[1]
	if c.Offers != 2 || c.Followed != 2 || c.Unseen != 0 {
		t.Errorf("compact_now = %+v", c)
	}
	if _, ok := c.UnseenRate(); ok {
		t.Error("two settled offers are not enough evidence for a rate")
	}
	if MostlyUnseen(Fold(entries, volNow), "compact_now", volNow) || MostlyUnseen(nil, "nothing", volNow) {
		t.Error("unseen claimed without evidence")
	}
}

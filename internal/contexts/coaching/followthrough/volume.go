package followthrough

import (
	"sort"
	"time"
)

// The measure behind "is this kind worth saying": how many tips of a kind
// reached the operator each day and how often nothing showed they were
// seen. Direction priority 1 judges every cut by these two figures.
const (
	// VolumeDays is the default look-back for the tip volume report.
	VolumeDays = 7
	// UnseenMinOffers is how many settled offers of a kind the unseen
	// rate needs before it is evidence; under it a rate is noise.
	UnseenMinOffers = 5
	// UnseenMostly is the unseen share at or above which a kind counts
	// as mostly unseen.
	UnseenMostly = 0.5
)

// KindVolume is the tips of one kind in the look-back.
type KindVolume struct {
	Kind string `json:"kind"`
	// Offers is every tip of the kind in the window.
	Offers int `json:"offers"`
	// PerDay counts offers in each trailing 24 hours, oldest first; the
	// last element is the 24 hours ending now.
	PerDay []int `json:"per_day"`
	// Unseen, Followed and Ignored are the settled outcomes; Open offers
	// have no outcome yet and Answered were answered by the operator.
	Unseen   int `json:"unseen"`
	Followed int `json:"followed"`
	Ignored  int `json:"ignored"`
	Answered int `json:"answered"`
	Open     int `json:"open"`
}

// Settled is how many offers have an outcome the rate can be read from.
func (v KindVolume) Settled() int { return v.Offers - v.Open }

// UnseenRate is the share of settled offers nothing showed were seen. ok
// is false while fewer than UnseenMinOffers have settled.
func (v KindVolume) UnseenRate() (rate float64, ok bool) {
	if v.Settled() < UnseenMinOffers {
		return 0, false
	}
	return float64(v.Unseen) / float64(v.Settled()), true
}

// MostlyUnseen reports that the evidence says most tips of the kind went
// unseen.
func (v KindVolume) MostlyUnseen() bool {
	r, ok := v.UnseenRate()
	return ok && r >= UnseenMostly
}

// Volume counts the tips offered in the last days days by kind, busiest
// first. Only tips count: advice, approvals and moves are answered in the
// agent's own turn, so "unseen" does not apply to them.
func Volume(records []Record, now time.Time, days int) []KindVolume {
	if days <= 0 {
		days = VolumeDays
	}
	byKind := map[string]*KindVolume{}
	for _, r := range records {
		age := now.Sub(r.Offer.At)
		if r.Offer.Channel != ChannelTip || age < 0 || age >= time.Duration(days)*24*time.Hour {
			continue
		}
		v := byKind[r.Offer.Kind]
		if v == nil {
			v = &KindVolume{Kind: r.Offer.Kind, PerDay: make([]int, days)}
			byKind[r.Offer.Kind] = v
		}
		v.Offers++
		v.PerDay[days-1-int(age/(24*time.Hour))]++
		switch r.Outcome {
		case OutcomeUnseen:
			v.Unseen++
		case OutcomeFollowed:
			v.Followed++
		case OutcomeIgnored:
			v.Ignored++
		case OutcomeOpen:
			v.Open++
		case OutcomeAcknowledged, OutcomeExpected, OutcomeSnoozed:
			v.Answered++
		}
	}
	out := make([]KindVolume, 0, len(byKind))
	for _, v := range byKind {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Offers != out[j].Offers {
			return out[i].Offers > out[j].Offers
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// MostlyUnseen reports whether tips of kind went mostly unseen over the
// last VolumeDays days: at least UnseenMinOffers settled and an unseen
// share of UnseenMostly or more.
func MostlyUnseen(records []Record, kind string, now time.Time) bool {
	for _, v := range Volume(records, now, VolumeDays) {
		if v.Kind == kind {
			return v.MostlyUnseen()
		}
	}
	return false
}

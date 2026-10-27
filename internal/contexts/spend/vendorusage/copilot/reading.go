package copilot

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// ErrUnrecognisedShape is a user record TokenOps cannot read a plan from:
// GitHub changed the response, or the seat reports an allowance without
// saying how much of it is used. It is a failed poll, never an empty
// reading.
var ErrUnrecognisedShape = errors.New("copilot user: response shape not recognised")

// CreditsUnit names GitHub AI Credits (1 credit = 0.01 USD).
const CreditsUnit = "AI credits"

// Meter is one Copilot allowance the vendor measures: the month's AI
// Credits on a usage-billed plan, premium requests on an annual plan that
// has not moved to credits, chat or completions on Copilot Free.
type Meter struct {
	// Key is the quota_snapshots key.
	Key string
	// Name is the window in words: "month (AI credits)".
	Name    string
	UsedPct float64
	// Credits marks a meter billed in GitHub AI Credits.
	Credits     bool
	Entitlement int
	CreditsUsed float64
	HasCredits  bool
}

// Reading is what one user record says about the operator's plan.
type Reading struct {
	// Meters are the buckets with an allowance, busiest first.
	Meters []Meter
	// Unmetered is a seat with no personal allowance in any bucket
	// (unlimited, entitlement 0): an organisation seat whose limit is the
	// organisation's budget, which this endpoint does not report.
	Unmetered bool
	// CreditsUsed is the AI Credits used this cycle across buckets, when
	// any bucket reports it.
	CreditsUsed float64
	HasCredits  bool
	// ResetsAt is when the allowances reset; zero when not reported.
	ResetsAt time.Time
}

// Reading classifies the record's buckets. A record with no buckets, or
// with a capped bucket that reports no share used, is ErrUnrecognisedShape
// with the reason, so the poll fails visibly instead of reporting nothing.
func (u *UserResponse) Reading() (Reading, error) {
	if len(u.QuotaSnapshots) == 0 {
		return Reading{}, fmt.Errorf("%w: no quota_snapshots in the response", ErrUnrecognisedShape)
	}
	r := Reading{ResetsAt: parseResetDate(u.QuotaResetDate)}
	keys := make([]string, 0, len(u.QuotaSnapshots))
	for k := range u.QuotaSnapshots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := u.QuotaSnapshots[k]
		if s.CreditsUsed != nil {
			r.CreditsUsed += *s.CreditsUsed
			r.HasCredits = true
		}
		if !s.Metered() {
			continue
		}
		m, ok := meterFor(k, s, u.TokenBasedBilling || s.TokenBasedBilling)
		if !ok {
			return Reading{}, fmt.Errorf("%w: %s has an allowance of %d but reports no share used", ErrUnrecognisedShape, k, s.Entitlement)
		}
		r.Meters = append(r.Meters, m)
	}
	sort.SliceStable(r.Meters, func(i, j int) bool { return r.Meters[i].UsedPct > r.Meters[j].UsedPct })
	r.Unmetered = len(r.Meters) == 0
	return r, nil
}

// meterFor reads one capped bucket. The vendor's own share wins; on a
// credit-billed bucket without one, credits used over the allowance stand
// in for it.
func meterFor(key string, s QuotaSnapshot, credits bool) (Meter, bool) {
	m := Meter{Key: key, Name: "month (" + bucketWords(key, credits) + ")", Credits: credits, Entitlement: s.Entitlement}
	if s.CreditsUsed != nil {
		m.CreditsUsed, m.HasCredits = *s.CreditsUsed, true
	}
	switch {
	case s.PercentRemaining != nil:
		m.UsedPct = 100 - *s.PercentRemaining
	case credits && m.HasCredits:
		m.UsedPct = m.CreditsUsed / float64(s.Entitlement) * 100
	default:
		return Meter{}, false
	}
	m.UsedPct = math.Max(0, m.UsedPct)
	return m, true
}

// bucketWords names a bucket: premium_interactions is the month's AI
// Credits on a usage-billed plan and premium requests on one that is not.
func bucketWords(key string, credits bool) string {
	if key == "premium_interactions" {
		if credits {
			return CreditsUnit
		}
		return "premium requests"
	}
	return strings.ReplaceAll(key, "_", " ")
}

// parseResetDate reads quota_reset_date: a plain date ("2026-09-01"),
// midnight UTC, or an RFC 3339 time.
func parseResetDate(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

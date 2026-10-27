package providersetup

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	fireworksusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/fireworks"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EventReader reads stored envelopes; the event store satisfies it.
type EventReader = plans.EventReader

// ReadingNote is what a provider's latest spend reading this month leaves
// out, in one line: that the vendor blocks requests at the limit and until
// when (local date), or why an account-wide figure stands in for the
// operator's own. Empty when neither. `tokenops vendor-usage status`
// shows it beside the source.
func ReadingNote(ctx context.Context, reader EventReader, provider eventschema.Provider, now time.Time) string {
	v := plans.LatestVendorSpend(ctx, reader, provider, now)
	switch {
	case v == nil:
		return ""
	case v.LimitReached && !v.BlockedUntil.IsZero() && v.BlockedUntil.After(now):
		return "blocked until " + v.BlockedUntil.Local().Format("2 Jan") + " by your spend limit"
	case v.Scope == "account" && v.ScopeReason != "":
		return "account-wide: " + v.ScopeReason
	}
	return ""
}

// ValidFireworksEmail reports whether email can find a Fireworks user:
// shaped like an address, and not like a Fireworks key.
func ValidFireworksEmail(email string) bool { return fireworksusage.ValidEmail(email) }

package plans

import (
	"context"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// UnmeteredSeat is a seat the vendor reports with no personal allowance:
// a Copilot organisation seat, every bucket unlimited with entitlement 0.
// Its limit is the organisation's budget, which the member's own endpoint
// does not report, so it has no share used and no headroom to show.
type UnmeteredSeat struct {
	// CreditsUsed is what the vendor says the member used this cycle,
	// when HasCredits.
	CreditsUsed float64
	HasCredits  bool
	Unit        string
}

// LatestUnmeteredSeat returns the seat when the newest plan reading for
// provider (seat=metered|unmetered, written once per poll) says it is
// unmetered, or nil. Only Copilot writes plan readings, so no other
// provider costs a month-long read.
func LatestUnmeteredSeat(ctx context.Context, reader EventReader, provider eventschema.Provider, now time.Time) *UnmeteredSeat {
	if provider != eventschema.ProviderGitHub {
		return nil
	}
	events, err := reader.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-35*24*time.Hour))
	if err != nil {
		return nil
	}
	var best *eventschema.Envelope
	for _, e := range events {
		if e == nil || e.Attributes["seat"] == "" || readingProvider(e) != provider {
			continue
		}
		if best == nil || e.Timestamp.After(best.Timestamp) {
			best = e
		}
	}
	if best == nil || best.Attributes["seat"] != "unmetered" {
		return nil
	}
	seat := &UnmeteredSeat{Unit: best.Attributes["used_credits_unit"]}
	if v, err := strconv.ParseFloat(best.Attributes["used_credits"], 64); err == nil {
		seat.CreditsUsed, seat.HasCredits = v, true
	}
	return seat
}

// unmeteredNote says what an unmetered seat's report can and cannot say.
func unmeteredNote(s *UnmeteredSeat) string {
	note := "the vendor reports no personal allowance for this seat (unlimited, entitlement 0): its limit is the organisation's budget, which is not reported here"
	if !s.HasCredits {
		return note + "; no usage reported this cycle"
	}
	unit := s.Unit
	if unit == "" {
		unit = "credits"
	}
	return note + "; " + strconv.FormatFloat(s.CreditsUsed, 'f', -1, 64) + " " + unit + " used this cycle"
}

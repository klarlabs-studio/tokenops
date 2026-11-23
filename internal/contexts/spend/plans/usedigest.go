package plans

import (
	"context"
	"slices"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// UseDigest is what plan use (PlanUseOn), the plan's window readings
// (QuotaReadingsFrom) and plan value (ListValue) read of a span's prompt
// events, kept without the events.
//
// The team upload measures each covered day's plan use from three weeks
// of prompt events. Read as envelopes, a busy machine's three weeks (most
// of them Codex turns, each carrying its window readings) built about
// 200 MB of them for the upload to hold while it measured. A digest is
// built in one scan, each event built into one reused envelope where the
// reader scans (EventScanner), and holds a few numbers per event: a
// reading's windows are shared with the reading before it when they are
// the same.
//
// Its answers are those of the functions it stands for over the events it
// was built from, in the same order.
type UseDigest struct {
	readings []digestReading
	turns    []digestTurn
	// valued is whether the turns at or after valueFrom were priced
	// (DigestUse's price).
	valued    bool
	valueFrom time.Time
}

// digestReading is one window reading.
type digestReading struct {
	at       time.Time
	provider eventschema.Provider
	windows  []QuotaWindow
	// plan is whether it reads the plan's windows (planReading).
	plan bool
}

// digestTurn is one per-request turn, with its list value when it was
// priced.
type digestTurn struct {
	at time.Time
	planTurn
	usd    float64
	priced bool
}

// DigestUse builds the digest of r's prompt events at or after since,
// scanning them when r scans. With price set, the plan-covered turns at or
// after valueFrom are priced at API list price for ListValue.
func DigestUse(ctx context.Context, r EventReader, since time.Time, price Pricer, valueFrom time.Time) (UseDigest, error) {
	b := newDigestBuilder(price, valueFrom)
	if err := scanEvents(ctx, r, since, b.add); err != nil {
		return UseDigest{}, err
	}
	return b.d, nil
}

// DigestOf is the digest of envs, as DigestUse builds it.
func DigestOf(envs []*eventschema.Envelope, price Pricer, valueFrom time.Time) UseDigest {
	b := newDigestBuilder(price, valueFrom)
	for _, e := range envs {
		if e != nil {
			b.add(e)
		}
	}
	return b.d
}

type digestBuilder struct {
	d         UseDigest
	price     Pricer
	valueFrom time.Time
	// last is each provider's last reading's windows.
	last map[eventschema.Provider][]QuotaWindow
}

func newDigestBuilder(price Pricer, valueFrom time.Time) *digestBuilder {
	return &digestBuilder{d: UseDigest{valued: price != nil, valueFrom: valueFrom}, price: price, valueFrom: valueFrom,
		last: map[eventschema.Provider][]QuotaWindow{}}
}

// add digests e, which add does not keep.
func (b *digestBuilder) add(e *eventschema.Envelope) {
	if p, ok := windowReadingProvider(e); ok {
		if ws := QuotaWindowsFromAttributes(p, e.Attributes); len(ws) > 0 {
			if prev := b.last[p]; slices.Equal(prev, ws) {
				ws = prev
			}
			b.last[p] = ws
			b.d.readings = append(b.d.readings, digestReading{at: e.Timestamp, provider: p, windows: ws, plan: planReading(e)})
		}
	}
	if t, ok := turnOf(e); ok {
		dt := digestTurn{at: e.Timestamp, planTurn: t}
		if b.price != nil && t.source == eventschema.CostSourcePlanIncluded && !e.Timestamp.Before(b.valueFrom) {
			dt.usd, dt.priced = listPrice(e, b.price)
		}
		b.d.turns = append(b.d.turns, dt)
	}
}

// PlanUseOn is PlanUseOn over the digest's events.
func (d UseDigest) PlanUseOn(start, end time.Time) PlanUseReport {
	readings := map[string][]QuotaWindow{}
	for _, r := range d.readings {
		if !r.at.After(end) {
			readings[string(r.provider)] = append(readings[string(r.provider)], r.windows...)
		}
	}
	plan, billed := map[string]int64{}, map[string]int64{}
	for _, t := range d.turns {
		if !t.at.Before(start) && !t.at.After(end) {
			t.count(plan, billed)
		}
	}
	return useOf(readings, plan, billed, start, end)
}

// QuotaReadings is QuotaReadingsFrom over the digest's events.
func (d UseDigest) QuotaReadings(provider string, since, until time.Time) []QuotaReading {
	var out []QuotaReading
	for _, r := range d.readings {
		if r.plan && string(r.provider) == provider && !r.at.Before(since) && !r.at.After(until) {
			out = append(out, QuotaReading{At: r.at, Windows: r.windows})
		}
	}
	sortReadings(out)
	return out
}

// ListValue is ListValue over the digest's events, with the price it was
// built with; zero when it was built without one. The turns before its
// valueFrom were not priced and are not counted.
func (d UseDigest) ListValue(provider string, since, until time.Time) (usd float64, unpriced int) {
	if !d.valued {
		return 0, 0
	}
	for _, t := range d.turns {
		if t.at.Before(since) || t.at.Before(d.valueFrom) || t.at.After(until) || t.provider != provider || t.source != eventschema.CostSourcePlanIncluded {
			continue
		}
		if !t.priced {
			unpriced++
			continue
		}
		usd += t.usd
	}
	return usd, unpriced
}

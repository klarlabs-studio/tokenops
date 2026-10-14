package savings

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

type memLedger struct{ entries []learning.SavingsEntry }

func (m *memLedger) Append(es ...learning.SavingsEntry) error {
	m.entries = append(m.entries, es...)
	return nil
}
func (m *memLedger) Load() ([]learning.SavingsEntry, error) { return m.entries, nil }

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// Every tip writes a row with its expected saving, evidence and decision
// ID; held tips are the holdout cohort, and their implicit outcomes are
// recorded against the same ID.
func TestRecordAndBuild(t *testing.T) {
	l := &memLedger{}
	peaks := map[string]float64{}
	for i := range 6 {
		tr, ho := fmt.Sprint("t", i), fmt.Sprint("h", i)
		RecordTip(l, t0, Tip{ID: tr, Session: tr, Kind: "compact_now", Cohort: learning.CohortTreated, ExpectedUSD: 0.3, ExpectedTokens: 600_000, Evidence: "context 700k", SessionUSD: 10})
		RecordTip(l, t0, Tip{ID: ho, Session: ho, Kind: "compact_now", Cohort: learning.CohortHoldout, ExpectedUSD: 0.3, Evidence: "context 700k", SessionUSD: 10})
		peaks[tr], peaks[ho] = 13, 19
	}
	RecordOutcomes(l, t0.Add(time.Minute), []Resolution{{ID: "h0", Signal: learning.SignalUnchanged}})
	if l.entries[0].Capability != learning.CapabilityCompaction || l.entries[0].ExpectedBasis == "" || l.entries[0].ID != "t0" {
		t.Fatalf("row = %+v", l.entries[0])
	}
	r := Build(context.Background(), Deps{Ledger: l, SessionPeaks: func() map[string]float64 { return peaks }}, t0.Add(48*time.Hour))
	if r.Interventions != 6 || r.Held != 6 || len(r.Kinds) != 1 {
		t.Fatalf("report = %+v", r)
	}
	if got, ok := r.RealizedUSD.Amount(); !ok || got < 35.9 || got > 36.1 {
		t.Fatalf("realized = %v; want $36 (6 tips x $6)", r.RealizedUSD)
	}
	line := Describe(r.Kinds[0])
	for _, want := range []string{"compact", "6 shown", "6 held back", "$6.00"} {
		if !strings.Contains(line, want) {
			t.Errorf("Describe = %q; missing %q", line, want)
		}
	}
}

// With nothing held back the realized saving is unknown, and the line
// says why instead of claiming the expected figure.
func TestDescribeWithoutHoldout(t *testing.T) {
	l := &memLedger{}
	RecordReadGuard(l, t0, "r1", "s", false, 4000)
	r := Build(context.Background(), Deps{Ledger: l}, t0)
	if r.RealizedUSD.Known() {
		t.Fatalf("realized = %v; want unknown", r.RealizedUSD)
	}
	line := Describe(r.Kinds[0])
	if !strings.Contains(line, "re-read") || !strings.Contains(line, "not established") {
		t.Fatalf("Describe = %q", line)
	}
}

// A ledger that cannot be read is an empty report, not an error that
// stops a command.
func TestBuildWithoutLedger(t *testing.T) {
	if r := Build(context.Background(), Deps{}, t0); len(r.Kinds) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

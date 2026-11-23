package avoidable

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

func TestRoutesAreTheModelOffers(t *testing.T) {
	got := routesIn([]ft.Entry{
		{Type: ft.EntryOffer, Power: config.PowerModels, Session: "s", Kind: "lookup", From: "opus", To: "haiku", At: t0},
		{Type: ft.EntryOffer, Power: config.PowerInform, Session: "s", Kind: "quota_90"},
		{Type: ft.EntryResolve, Power: config.PowerModels, Session: "s", From: "opus", To: "haiku"},
		{Type: ft.EntryOffer, Power: config.PowerModels, Session: "", From: "opus", To: "haiku"},
	})
	if len(got) != 1 || got[0].From != "opus" || got[0].To != "haiku" || got[0].Kind != "lookup" {
		t.Errorf("routes = %+v", got)
	}
}

type memLedger []learning.SavingsEntry

func (m memLedger) Append(...learning.SavingsEntry) error  { return nil }
func (m memLedger) Load() ([]learning.SavingsEntry, error) { return m, nil }

// The period's interventions, with their outcomes whenever they came.
func TestPeriodLedgerKeepsThePeriodsInterventions(t *testing.T) {
	l := periodLedger{Ledger: memLedger{
		{Type: learning.SavingsIntervention, ID: "before", At: t0.Add(-day)},
		{Type: learning.SavingsIntervention, ID: "in", At: t0.Add(time.Hour)},
		{Type: learning.SavingsOutcome, ID: "in", At: t0.Add(8 * day)},
		{Type: learning.SavingsIntervention, ID: "after", At: t0.Add(8 * day)},
	}, since: t0, until: t0.Add(7 * day)}
	got, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "in" || got[1].Type != learning.SavingsOutcome {
		t.Errorf("entries = %+v", got)
	}
}

// The daemon stores the week's report; the glance reads it back whole.
func TestSnapshotRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if s, err := ReadSnapshot(dir); s != nil || err != nil {
		t.Fatalf("before any run: %v %v", s, err)
	}
	r := Build(fixture())
	if err := WriteSnapshot(dir, Snapshot{ComputedAt: t0, Report: r}); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSnapshot(dir)
	if err != nil || s == nil {
		t.Fatalf("read: %v %v", s, err)
	}
	if s.Report.Headline != r.Headline || s.Report.Avoidable.Tokens != r.Avoidable.Tokens || !s.ComputedAt.Equal(t0) {
		t.Errorf("snapshot = %+v", s.Report)
	}
}

// Compute reads every source through its Deps; nil ones are left out.
func TestComputeReadsThroughDeps(t *testing.T) {
	in := fixture()
	d := Deps{
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return in.Turns, nil },
		Dirs:  func(time.Time) map[string]sessiondirs.Dir { return map[string]sessiondirs.Dir{} },
		Rereads: func() ([]Reread, []Reread) {
			return in.Rereads, in.Refused
		},
		Routes: func() []RouteAdvice { return in.Routes },
		Price:  price,
		Steps: func(context.Context, time.Time) []savings.Step {
			return []savings.Step{step(learning.CapabilityReadGuard, "waste", learning.StageShadow, true)}
		},
	}
	r, err := Compute(context.Background(), d, in.Since, in.Until)
	if err != nil {
		t.Fatal(err)
	}
	if r.Usage.Tokens != 8_000 || r.Next == nil || r.Next.Capability != learning.CapabilityReadGuard {
		t.Errorf("report = %+v", r)
	}
	// No session directory is known, so no session is judged abandoned.
	for _, c := range r.Classes {
		if c.Class == "abandoned_session" && c.Tokens != 0 {
			t.Errorf("abandoned without a commit join: %+v", c)
		}
	}
	if r.Realized.RefusedRereads != 1 {
		t.Errorf("realized = %+v", r.Realized)
	}
}

// The wired report keeps its git lookups between reports, so a daemon
// does not resolve thousands of session directories afresh on each.
func TestDepsForKeepsItsGitLookups(t *testing.T) {
	if DepsFor(config.Config{}, nil, nil, nil).Repos == nil {
		t.Fatal("DepsFor resolves repositories afresh on every report")
	}
}

// A past period's transcript records are that period's: a week's figures
// computed weeks later hold its own records, not every one written since.
// The daemon's weekly figures held months of records for the oldest week.
func TestTranscriptRecordsAreThePeriods(t *testing.T) {
	root := t.TempDir()
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	var b strings.Builder
	// One session, an instruction a day for three weeks.
	for d := range 21 {
		at := start.AddDate(0, 0, d).Add(9 * time.Hour).Format(time.RFC3339Nano)
		fmt.Fprintf(&b, `{"type":"user","timestamp":%q,"sessionId":"s","message":{"content":"task %d"}}`+"\n", at, d)
	}
	path := filepath.Join(root, "p", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	since, until := start, start.AddDate(0, 0, 7)
	recs, err := transcriptRecords(root, nil)(context.Background(), since, until)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 7 {
		t.Errorf("read %d records for the week, want its 7", len(recs))
	}
	for _, r := range recs {
		if r.At.Before(since) || !r.At.Before(until) {
			t.Errorf("record at %s outside the week", r.At)
		}
	}
}

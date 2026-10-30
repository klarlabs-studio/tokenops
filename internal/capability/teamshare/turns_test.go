package teamshare

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// One upload reads the event store's turns once, over the widest span a
// section needs, and hands each section the turns from its own start: the
// same rows scanned three times an upload ran it past its deadline.
func TestUploadReadsTheTurnsOnce(t *testing.T) {
	first, _ := time.Parse(teamwire.DayLayout, CoveredDays(now, 14)[0])
	old := analytics.SessionTurn{SessionID: "s-old", Provider: "anthropic", At: first.AddDate(0, 0, -20), Tokens: 1}
	recent := analytics.SessionTurn{SessionID: "s-new", Provider: "anthropic", At: first.Add(time.Hour), Tokens: 1}
	d := spendDeps()
	var reads []time.Time
	d.Turns = func(_ context.Context, since time.Time) ([]analytics.SessionTurn, error) {
		reads = append(reads, since)
		var out []analytics.SessionTurn
		for _, tr := range []analytics.SessionTurn{old, recent} {
			if !tr.At.Before(since) {
				out = append(out, tr)
			}
		}
		return out, nil
	}
	var wasteGot, placedGot int
	d.FinOps.Waste = func(ctx context.Context, turns TurnReader, since, _ time.Time) ([]avoidable.Day, error) {
		ts, err := turns(ctx, since.AddDate(0, 0, -avoidable.CalibrationDays))
		wasteGot = len(ts)
		return nil, err
	}
	d.Schema3.Placed = func(ctx context.Context, turns TurnReader, since, _ time.Time) ([]uniteconomics.Placed, error) {
		ts, err := turns(ctx, since.Add(-uniteconomics.PRMaxGap))
		placedGot = len(ts)
		return nil, err
	}
	u, warnings := Build(context.Background(), d, Options{Days: 14, Schema: teamwire.SchemaSpend}, now)
	if len(warnings) > 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if len(reads) != 1 || !reads[0].Equal(first.AddDate(0, 0, -avoidable.CalibrationDays)) {
		t.Fatalf("turn reads = %v, want one from %s", reads, first.AddDate(0, 0, -avoidable.CalibrationDays))
	}
	if wasteGot != 2 {
		t.Errorf("the waste report got %d turns, want both", wasteGot)
	}
	if placedGot != 1 {
		t.Errorf("unit economics got %d turns, want only the one within a week of the first day", placedGot)
	}
	for _, b := range u.Buckets {
		if b.Day < u.Days[0] {
			t.Errorf("a bucket before the covered days: %+v", b)
		}
	}
}

// A section that reaches further back than the shared read reads for
// itself rather than getting a short answer.
func TestSharedTurnsReadsWhatItDoesNotHold(t *testing.T) {
	var reads int
	read := func(_ context.Context, since time.Time) ([]analytics.SessionTurn, error) {
		reads++
		return []analytics.SessionTurn{{At: since}}, nil
	}
	s := &sharedTurns{read: read}
	ctx := context.Background()
	if _, err := s.prime(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Read(ctx, now.Add(time.Hour)); len(got) != 0 || reads != 1 {
		t.Fatalf("a later start: %d turns, %d reads", len(got), reads)
	}
	if got, _ := s.Read(ctx, now.Add(-time.Hour)); len(got) != 1 || reads != 2 {
		t.Fatalf("an earlier start: %d turns, %d reads", len(got), reads)
	}
}

// An upload tells Took how long each part took to read, so one that runs
// out of time names the part that used it.
func TestUploadTimesEachPart(t *testing.T) {
	var parts []string
	opts := Options{Days: 14, RepoNames: RepoRemote, Schema: teamwire.SchemaSpend, Role: "owner",
		Took: func(part string, _ time.Duration) { parts = append(parts, part) }}
	if _, warnings := Build(context.Background(), spendDeps(), opts, now); len(warnings) > 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	want := []string{"transcripts", "turns", "repositories", "plan days", "plan advice", "waste", "units of work", "vendor cost"}
	if !slices.Equal(parts, want) {
		t.Errorf("parts timed = %q, want %q", parts, want)
	}
}

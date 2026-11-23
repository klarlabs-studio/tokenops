package teamshare

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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
	u, notes := Build(context.Background(), d, Options{Days: 14, Schema: teamwire.SchemaSpend}, now)
	if len(notes.All()) > 0 {
		t.Fatalf("notes: %v", notes.All())
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
	if _, notes := Build(context.Background(), spendDeps(), opts, now); len(notes.All()) > 0 {
		t.Fatalf("notes: %v", notes.All())
	}
	want := []string{"plan days", "plan advice", "plan windows", "transcripts", "turns", "repositories", "waste", "units of work", "vendor cost"}
	if !slices.Equal(parts, want) {
		t.Errorf("parts timed = %q, want %q", parts, want)
	}
}

// The turns an upload holds are handed to each section as they are: the
// sections only read them, and a copy of the waste report's six weeks of
// a busy machine's turns was a few hundred megabytes more at the
// upload's peak. A section that appends gets its own slice.
func TestSharedTurnsHandsOverTheHeldTurns(t *testing.T) {
	held := make([]analytics.SessionTurn, 1000)
	for i := range held {
		held[i] = analytics.SessionTurn{SessionID: "s", At: now.Add(time.Duration(i) * time.Minute), Tokens: 1}
	}
	s := &sharedTurns{read: func(context.Context, time.Time) ([]analytics.SessionTurn, error) {
		return slices.Clone(held), nil
	}}
	ctx := context.Background()
	if _, err := s.prime(ctx, now); err != nil {
		t.Fatal(err)
	}
	from := held[500].At
	if allocs := testing.AllocsPerRun(10, func() { _, _ = s.Read(ctx, from) }); allocs != 0 {
		t.Errorf("a read of the held turns allocated %v times, want none", allocs)
	}
	got, _ := s.Read(ctx, from)
	if len(got) != 500 || !got[0].At.Equal(from) {
		t.Fatalf("read from the 500th turn: %d turns from %s", len(got), got[0].At)
	}
	_ = append(got, analytics.SessionTurn{SessionID: "appended"})
	if again, _ := s.Read(ctx, from); len(again) != 500 || len(s.turns) != 1000 {
		t.Errorf("an append to a section's turns changed the held ones: %d, %d", len(again), len(s.turns))
	}
}

// readsWatched is a plan reader that calls read before each of its reads.
type readsWatched struct {
	eventsReader
	read func()
}

func (r readsWatched) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	r.read()
	return r.eventsReader.ReadEvents(ctx, t, since)
}

// An upload reads the plan events before the turns: the two largest
// reads of an upload, weeks of prompt events and weeks of turns, are not
// held at once.
func TestUploadReadsThePlansBeforeTheTurns(t *testing.T) {
	d := spendDeps()
	var turnsRead, plansAfter bool
	turns := d.Turns
	d.Turns = func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
		turnsRead = true
		return turns(ctx, since)
	}
	plansOf := d.FinOps.Plans
	d.FinOps.Plans = func() headroom.Deps {
		p := plansOf()
		p.Reader = readsWatched{p.Reader.(eventsReader), func() { plansAfter = plansAfter || turnsRead }}
		return p
	}
	u, notes := Build(context.Background(), d, Options{Days: 14, RepoNames: RepoRemote, Schema: teamwire.SchemaVersion}, now)
	if len(notes.All()) > 0 {
		t.Fatalf("notes: %v", notes.All())
	}
	if len(u.PlanDays) == 0 || !turnsRead {
		t.Fatalf("the upload read no plans or no turns: %+v", u.PlanDays)
	}
	if plansAfter {
		t.Error("the plan events were read while the turns were held")
	}
}

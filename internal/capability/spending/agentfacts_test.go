package spending

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/tools"
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func toolUse(session string, at time.Time, id, name, command string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"sessionId":%q,"message":{"content":[{"type":"text","text":"on it"},{"type":"tool_use","id":%q,"name":%q,"input":{"command":%q}}]}}`,
		at.Format(time.RFC3339), session, id, name, command)
}

func toolResult(session string, at time.Time, id string, failed bool) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":%q,"message":{"content":[{"type":"tool_result","tool_use_id":%q,"is_error":%t,"content":"out"}]}}`,
		at.Format(time.RFC3339), session, id, failed)
}

// typedParts is a prompt in the array form current Claude Code writes.
func typedParts(session string, at time.Time, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":%q,"message":{"content":[{"type":"text","text":%q}]}}`,
		at.Format(time.RFC3339), session, text)
}

func repeated(n int, line func(i int) string) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, line(i))
	}
	return out
}

// writeAt writes one transcript at an exact path under root.
func writeAt(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// twoExtracts is how the agent KPIs were computed before AgentFacts:
// every prompt and every tool event from baselineSince extracted in full,
// in two walks of the transcripts, then analysed.
func twoExtracts(t *testing.T, root string, baselineSince, since time.Time) (period, baseline scorecard.AgentKPIInputs) {
	t.Helper()
	typed, err := prompts.Extract(prompts.ExtractOptions{Root: root, Since: baselineSince})
	if err != nil {
		t.Fatal(err)
	}
	called, err := tools.Extract(tools.ExtractOptions{Root: root, Since: baselineSince})
	if err != nil {
		t.Fatal(err)
	}
	of := func(in func(time.Time) bool) scorecard.AgentKPIInputs {
		type key struct{ session, text string }
		sentinel := map[string]bool{"continue": true, "proceed": true, "keep going": true}
		counts := map[key]int{}
		var ps []prompts.UserPrompt
		for _, p := range typed {
			if in(p.Timestamp) {
				ps = append(ps, p)
				if lc := strings.ToLower(strings.TrimSpace(p.Text)); sentinel[lc] {
					counts[key{p.SessionID, lc}]++
				}
			}
		}
		var kept []prompts.UserPrompt
		for _, p := range ps {
			lc := strings.ToLower(strings.TrimSpace(p.Text))
			if !sentinel[lc] || counts[key{p.SessionID, lc}] <= loopSentinelRepeats {
				kept = append(kept, p)
			}
		}
		if len(kept) == 0 {
			return scorecard.AgentKPIInputs{}
		}
		f := prompts.Analyze(kept)
		out := scorecard.AgentKPIInputs{
			ConfirmationGateRatePct:  100.0 * float64(f.Acknowledgements) / float64(f.TotalPrompts),
			ConfirmationGateComputed: true,
			RegenerateRatePct:        100.0 * float64(f.Regenerates) / float64(f.TotalPrompts),
			RegenerateComputed:       true,
		}
		var evs []tools.ToolEvent
		for _, e := range called {
			if in(e.Timestamp) {
				evs = append(evs, e)
			}
		}
		if ts := tools.Analyze(evs); ts.TotalToolCalls > 0 {
			out.ToolSuccessRatePct, out.ToolSuccessComputed = ts.SuccessRate, true
			out.DestructiveRatePct, out.DestructiveComputed = ts.DestructiveRate, true
		}
		return out
	}
	return of(func(at time.Time) bool { return !at.Before(since) }),
		of(func(at time.Time) bool { return at.Before(since) })
}

// agentFixture is a month of sessions: an operator at work, a /loop, the
// weeks before, a session older than the baseline and one a harness ran
// in a throwaway directory.
func agentFixture(t *testing.T, root string, now time.Time) {
	t.Helper()
	at, earlier := now.Add(-2*time.Hour), now.AddDate(0, 0, -10)
	min := func(base time.Time, i int) time.Time { return base.Add(time.Duration(i) * time.Minute) }

	human := make([]string, 0, 15)
	human = append(human,
		userTurn("human", at, "refactor the retry path to use the shared backoff"),
		toolUse("human", min(at, 1), "t1", "Bash", "go test ./..."),
		toolResult("human", min(at, 2), "t1", true),
		userTurn("human", min(at, 3), " OK "),
		toolUse("human", min(at, 4), "t2", "Bash", "rm -rf build"),
		toolResult("human", min(at, 5), "t2", false),
		userTurn("human", min(at, 6), "that's wrong, try again"),
		toolUse("human", min(at, 7), "t3", "Read", ""),
		typedParts("human", min(at, 8), "add a test for the jitter bound"),
		userTurn("human", min(at, 9), "<system-reminder>not the operator</system-reminder>"),
		`{"type":"user","timestamp":"not a time","sessionId":"human","message":{"content":"ok"}}`,
		`not json`,
	)
	// Under the threshold: the operator steering, counted.
	human = append(human, repeated(3, func(i int) string { return userTurn("human", min(at, 10+i), "continue") })...)
	writeTranscript(t, root, "human", human)

	// A /loop: more than the threshold in the period and in the baseline
	// are pacing, and four in each are the operator's.
	loop := repeated(8, func(i int) string { return userTurn("loop", min(at, i), "continue") })
	loop = append(loop, repeated(7, func(i int) string { return userTurn("loop", min(earlier, i), "Proceed") })...)
	loop = append(loop, repeated(4, func(i int) string { return userTurn("loop", min(at, 20+i), "keep going") })...)
	loop = append(loop, repeated(4, func(i int) string { return userTurn("loop", min(earlier, 20+i), "keep going") })...)
	writeTranscript(t, root, "loop", loop)

	writeTranscript(t, root, "before", []string{
		userTurn("before", earlier, "split the parser into two passes"),
		userTurn("before", min(earlier, 1), "yes"),
		toolUse("before", min(earlier, 2), "b1", "Edit", ""),
		toolResult("before", min(earlier, 3), "b1", true),
		userTurn("before", min(earlier, 4), "nope"),
		toolUse("before", min(earlier, 5), "b2", "Bash", "ls"),
		toolResult("before", min(earlier, 6), "b2", true),
		userTurn("before", min(earlier, 7), "add the golden files"),
	})
	// One session across the boundary between baseline and period, with
	// a prompt at the very start of the period, which is the period's.
	writeTranscript(t, root, "long", []string{
		userTurn("long", earlier, "ok"),
		userTurn("long", now.AddDate(0, 0, -7), "yes"),
		userTurn("long", at, "now document the new option"),
	})
	// Before the baseline, in a file written since: in neither window.
	writeTranscript(t, root, "old", []string{
		userTurn("old", now.AddDate(0, 0, -40), "ok"),
		toolUse("old", now.AddDate(0, 0, -40), "o1", "Bash", "sudo rm x"),
	})
	// A harness prompting in a throwaway directory is not the operator;
	// its tool calls are the agent's all the same.
	writeAt(t, filepath.Join(root, "-private-var-folders-wz-abc-T-worksim-claudecode-1", "sim.jsonl"), []string{
		userTurn("sim", at, "ok"),
		userTurn("sim", min(at, 1), "ok"),
		toolUse("sim", min(at, 2), "s1", "Bash", "git push --force"),
	})
}

func TestAgentKPIsMatchTwoFullExtracts(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	baselineSince := since.AddDate(0, 0, -scorecard.BaselineDays)
	root := t.TempDir()
	agentFixture(t, root, now)

	period, baseline := new(AgentFacts).kpis(context.Background(), root, baselineSince, since, now)
	wantPeriod, wantBaseline := twoExtracts(t, root, baselineSince, since)
	if period != wantPeriod {
		t.Errorf("period\n got %+v\nwant %+v", period, wantPeriod)
	}
	if baseline != wantBaseline {
		t.Errorf("baseline\n got %+v\nwant %+v", baseline, wantBaseline)
	}
	// The fixture, counted by hand, so the two cannot agree on nothing.
	// Period: 13 prompts (9 acknowledgements, 1 rejection), 4 tool calls
	// (2 destructive, 1 failed). Baseline: 9 prompts (6, 1), 2 calls,
	// both failed.
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"period CGR", period.ConfirmationGateRatePct, 100.0 * 9 / 13},
		{"period RGR", period.RegenerateRatePct, 100.0 * 1 / 13},
		{"period TCS", period.ToolSuccessRatePct, 75},
		{"period DAR", period.DestructiveRatePct, 50},
		{"baseline CGR", baseline.ConfirmationGateRatePct, 100.0 * 6 / 9},
		{"baseline RGR", baseline.RegenerateRatePct, 100.0 * 1 / 9},
		{"baseline TCS", baseline.ToolSuccessRatePct, 0},
		{"baseline DAR", baseline.DestructiveRatePct, 0},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !period.ToolSuccessComputed || !period.DestructiveComputed || !baseline.ToolSuccessComputed {
		t.Errorf("tool KPIs not computed: %+v / %+v", period, baseline)
	}
}

// A window with tool calls and no prompt the operator typed is not
// graded: what the agent did is measured against what it was asked.
func TestAgentKPIsNeedAPrompt(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	baselineSince := since.AddDate(0, 0, -scorecard.BaselineDays)
	root := t.TempDir()
	writeTranscript(t, root, "agent", []string{
		toolUse("agent", now.Add(-time.Hour), "t1", "Bash", "rm -rf build"),
		toolResult("agent", now.Add(-time.Hour), "t1", true),
		userTurn("agent", now.AddDate(0, 0, -10), "refactor the retry path"),
		toolUse("agent", now.AddDate(0, 0, -10), "t2", "Read", ""),
	})
	period, baseline := new(AgentFacts).kpis(context.Background(), root, baselineSince, since, now)
	wantPeriod, wantBaseline := twoExtracts(t, root, baselineSince, since)
	if period != wantPeriod || baseline != wantBaseline {
		t.Errorf("got %+v / %+v\nwant %+v / %+v", period, baseline, wantPeriod, wantBaseline)
	}
	if period != (scorecard.AgentKPIInputs{}) {
		t.Errorf("period graded with no prompt: %+v", period)
	}
	if !baseline.ToolSuccessComputed || baseline.ToolSuccessRatePct != 100 {
		t.Errorf("baseline %+v, want its one call graded", baseline)
	}
}

// Codex stamps its sessions in the file name and not always on the line,
// and its transcripts hold prompts only.
func TestAgentKPIsReadCodexPrompts(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	baselineSince := since.AddDate(0, 0, -scorecard.BaselineDays)
	root := filepath.Join(t.TempDir(), ".codex", "sessions")
	writeAt(t, filepath.Join(root, "2026", "rollout-2026-10-07T10-00-00-abc.jsonl"), []string{
		`{"role":"user","content":[{"type":"input_text","text":"ok"}]}`,
		`{"role":"user","timestamp":"2026-09-20T10:00:00Z","content":[{"type":"input_text","text":"wire the exporter to the new sink"}]}`,
		`{"role":"assistant","content":[{"type":"output_text","text":"done"}]}`,
		`{"role":"user","content":[{"type":"input_text","text":"rename the flag to match the docs"}]}`,
	})
	// Neither a line time nor one in the name: in no window.
	writeAt(t, filepath.Join(root, "2026", "unnamed.jsonl"), []string{
		`{"role":"user","content":[{"type":"input_text","text":"ok"}]}`,
	})

	period, baseline := new(AgentFacts).kpis(context.Background(), root, baselineSince, since, now)
	wantPeriod, wantBaseline := twoExtracts(t, root, baselineSince, since)
	if period != wantPeriod || baseline != wantBaseline {
		t.Errorf("got %+v / %+v\nwant %+v / %+v", period, baseline, wantPeriod, wantBaseline)
	}
	if period.ConfirmationGateRatePct != 50 || !baseline.ConfirmationGateComputed || baseline.ConfirmationGateRatePct != 0 {
		t.Errorf("period %+v, baseline %+v: want 1 acknowledgement in 2 prompts, then 0 in 1", period, baseline)
	}
	if period.ToolSuccessComputed || baseline.ToolSuccessComputed {
		t.Errorf("tool KPIs from a Codex transcript: %+v / %+v", period, baseline)
	}
}

// A scorecard after the first reads what the transcripts gained and
// nothing else: each one used to parse five weeks of them, twice.
func TestAgentFactsReadOnlyWhatWasWrittenSince(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	at := now.Add(-time.Hour)
	writeTranscript(t, root, "a", []string{
		userTurn("a", at, "refactor the retry path to use the shared backoff"),
		userTurn("a", at.Add(time.Minute), "ok"),
	})
	writeTranscript(t, root, "b", []string{
		userTurn("b", at, "add a test for the jitter bound"),
		userTurn("b", at.Add(time.Minute), "now document the new option"),
	})
	held := &AgentFacts{}
	cgr := func() float64 {
		t.Helper()
		s := Scorecard(context.Background(), nil, ScorecardParams{TranscriptRoot: root, Now: now, AgentFacts: held})
		return s.ConfirmationGateRate.Value
	}
	if got := cgr(); got != 25 {
		t.Fatalf("CGR %v, want 25", got)
	}
	if held.tail.FilesOpened != 2 {
		t.Fatalf("first scorecard opened %d transcripts, want 2", held.tail.FilesOpened)
	}
	read := held.tail.BytesParsed

	if got := cgr(); got != 25 {
		t.Fatalf("second CGR %v, want 25", got)
	}
	if held.tail.FilesOpened != 2 || held.tail.BytesParsed != read {
		t.Fatalf("a scorecard with nothing new opened %d transcripts and parsed %d bytes more, want none",
			held.tail.FilesOpened-2, held.tail.BytesParsed-read)
	}

	// One session goes on; the line being written is not a line yet.
	more := userTurn("a", at.Add(2*time.Minute), "yes") + "\n"
	half := userTurn("a", at.Add(3*time.Minute), "go ahead")
	path := filepath.Join(root, "proj", "a.jsonl")
	appendTo(t, path, more+half[:20])
	if got := cgr(); got != 40 {
		t.Fatalf("CGR %v after one more acknowledgement, want 40 (2 in 5)", got)
	}
	if held.tail.FilesOpened != 3 || held.tail.BytesParsed != read+int64(len(more)) {
		t.Fatalf("opened %d transcripts and parsed %d bytes for one appended line of %d",
			held.tail.FilesOpened-2, held.tail.BytesParsed-read, len(more))
	}
	appendTo(t, path, half[20:]+"\n")
	if got := cgr(); got != 50 {
		t.Fatalf("CGR %v once the line is whole, want 50 (3 in 6)", got)
	}

	// A transcript rewritten is read again, in place of what it held.
	writeTranscript(t, root, "a", []string{userTurn("a", at, "start over with the smaller design")})
	if got := cgr(); got != 0 {
		t.Fatalf("CGR %v after the session was rewritten, want 0 (none in 3)", got)
	}
	// One that is gone is forgotten.
	if err := os.Remove(filepath.Join(root, "proj", "b.jsonl")); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, root, "c", []string{userTurn("c", at, "ok")})
	if got := cgr(); got != 50 {
		t.Fatalf("CGR %v with one session gone and one new, want 50 (1 in 2)", got)
	}
	if len(held.files) != 2 || held.tail.Tracked() != 2 {
		t.Errorf("holds %d transcripts (%d followed), want the 2 that exist", len(held.files), held.tail.Tracked())
	}
}

// A week's scorecard after a month's keeps what the month's read: asked
// in turn, each would otherwise read again what the other dropped.
func TestAgentFactsKeepTheWidestWindowAsked(t *testing.T) {
	now := time.Now()
	root := t.TempDir()
	writeTranscript(t, root, "week", []string{
		userTurn("week", now.Add(-time.Hour), "refactor the retry path to use the shared backoff"),
		userTurn("week", now.Add(-time.Hour), "ok"),
	})
	// Beyond a week's scorecard's reach (its week and four before), within
	// a month's, as its baseline.
	writeTranscript(t, root, "month", []string{userTurn("month", now.AddDate(0, 0, -45), "ok")})
	old := filepath.Join(root, "proj", "month.jsonl")
	if err := os.Chtimes(old, now.AddDate(0, 0, -45), now.AddDate(0, 0, -45)); err != nil {
		t.Fatal(err)
	}
	// Written before any window asked here: never read.
	writeTranscript(t, root, "ancient", []string{userTurn("ancient", now.AddDate(0, 0, -200), "ok")})
	ancient := filepath.Join(root, "proj", "ancient.jsonl")
	if err := os.Chtimes(ancient, now.AddDate(0, 0, -200), now.AddDate(0, 0, -200)); err != nil {
		t.Fatal(err)
	}

	held := &AgentFacts{}
	cgr := func(days int) float64 {
		t.Helper()
		s := Scorecard(context.Background(), nil, ScorecardParams{TranscriptRoot: root, Now: now, SinceDays: days, AgentFacts: held})
		return s.ConfirmationGateRate.Value
	}
	for _, c := range []struct {
		days int
		want float64
	}{{30, 50}, {7, 50}, {30, 50}, {7, 50}} {
		if got := cgr(c.days); got != c.want {
			t.Fatalf("%d-day CGR %v, want %v", c.days, got, c.want)
		}
		if held.tail.FilesOpened != 2 {
			t.Fatalf("after a %d-day scorecard %d transcripts were opened in all, want the 2 in reach, once each", c.days, held.tail.FilesOpened)
		}
	}
}

// A caller that stops waiting does not stop a read whose result is kept:
// the scorecard that timed out once would time out every time.
func TestAgentFactsFinishTheReadForTheNextCaller(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	at := now.Add(-time.Hour)
	writeTranscript(t, root, "a", []string{userTurn("a", at, "ok")})
	writeTranscript(t, root, "b", []string{userTurn("b", at, "add a test for the jitter bound")})
	writeTranscript(t, root, "c", []string{userTurn("c", at, "now document the new option")})

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	held := &AgentFacts{}
	Scorecard(gone, nil, ScorecardParams{TranscriptRoot: root, Now: now, AgentFacts: held})
	if held.tail.Tracked() != 3 {
		t.Fatalf("a scorecard nobody waited for left %d of 3 transcripts read", held.tail.Tracked())
	}
	s := Scorecard(context.Background(), nil, ScorecardParams{TranscriptRoot: root, Now: now, AgentFacts: held})
	if got := s.ConfirmationGateRate.Value; got < 33.3 || got > 33.4 {
		t.Errorf("CGR %v, want a third", got)
	}
	if held.tail.FilesOpened != 3 {
		t.Errorf("the next scorecard opened %d transcripts again", held.tail.FilesOpened-3)
	}
}

// The facts are flags and times: no prompt text and no command is held
// between two scorecards.
func TestAgentFactsHoldNoText(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	at := now.Add(-time.Hour)
	writeTranscript(t, root, "s", []string{
		userTurn("s", at, "the launch code is hunter2"),
		toolUse("s", at.Add(time.Minute), "t1", "Bash", "rm -rf /secret/place"),
		userTurn("s", at.Add(2*time.Minute), "continue"),
	})
	held := &AgentFacts{}
	Scorecard(context.Background(), nil, ScorecardParams{TranscriptRoot: root, Now: now, AgentFacts: held})
	got := fmt.Sprintf("%+v", held.files)
	for _, private := range []string{"hunter2", "launch", "secret"} {
		if strings.Contains(got, private) {
			t.Errorf("held facts contain %q: %s", private, got)
		}
	}
	if !strings.Contains(got, "continue") {
		t.Errorf("the loop sentinel is not held: %s", got)
	}
}

// eventsAsked is an event reader that records what it was asked for.
type eventsAsked struct {
	types []eventschema.EventType
	since []time.Time
}

func (e *eventsAsked) ReadEvents(_ context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	e.types = append(e.types, t)
	e.since = append(e.since, since)
	return nil, nil
}

// The wedge KPIs read through the reader the caller holds, the store only
// without one: the daemon's is a cache, and the store's answer is five
// weeks of events decoded for each scorecard.
func TestScorecardReadsEventsThroughTheCallersReader(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, st := range []*sqlite.Store{nil, store} {
		asked := &eventsAsked{}
		Scorecard(context.Background(), st, ScorecardParams{TranscriptRoot: t.TempDir(), Now: now, Events: asked})
		if len(asked.types) != 2 || asked.types[0] != eventschema.EventTypePrompt || asked.types[1] != eventschema.EventTypeOptimization {
			t.Fatalf("store %v: the reader was asked for %v, want prompts then optimizations", st != nil, asked.types)
		}
		if want := now.AddDate(0, 0, -7-scorecard.BaselineDays); !asked.since[0].Equal(want) {
			t.Errorf("store %v: read from %s, want %s", st != nil, asked.since[0], want)
		}
	}
}

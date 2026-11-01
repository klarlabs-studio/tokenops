package fmtinsight

import (
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn"
	"go.klarlabs.de/tokenops/internal/infra/fmtindex"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestParseExperiment(t *testing.T) {
	for in, want := range map[string]int{"off": 0, "on": 50, "25": 25, "50%": 50, " ON ": 50} {
		got, err := ParseExperiment(in)
		if err != nil || got != want {
			t.Errorf("ParseExperiment(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"51", "-1", "half", ""} {
		if _, err := ParseExperiment(in); err == nil {
			t.Errorf("ParseExperiment(%q) accepted", in)
		}
	}
}

// transcript writes one session's transcript records: units instructions,
// the first of which comes back to a file it had moved on from, and one
// command the agent runs twice.
func transcript(id string, at time.Time, units int) []agentdx.Record {
	var out []agentdx.Record
	add := func(r agentdx.Record) {
		at = at.Add(time.Second)
		r.At, r.SessionID = at, id
		out = append(out, r)
	}
	for u := range units {
		add(agentdx.Record{Kind: agentdx.KindPrompt})
		add(agentdx.Record{Kind: agentdx.KindAssistantTurn, InputTokens: 1000})
		if u == 0 {
			add(agentdx.Record{Kind: agentdx.KindToolUse, ToolName: "Edit", FilePath: "a.go", CallSignature: "Edit a"})
			add(agentdx.Record{Kind: agentdx.KindToolUse, ToolName: "Edit", FilePath: "b.go", CallSignature: "Edit b"})
			add(agentdx.Record{Kind: agentdx.KindToolUse, ToolName: "Edit", FilePath: "a.go", CallSignature: "Edit a2"})
		}
		add(agentdx.Record{Kind: agentdx.KindToolUse, ToolName: "Bash", CallSignature: fmt.Sprintf("Bash go test %d", min(u, 1))})
		add(agentdx.Record{Kind: agentdx.KindAssistantTurn, InputTokens: 2000})
	}
	return out
}

func TestSessionOutcomesJoinsRunsToTranscripts(t *testing.T) {
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	runs := []fmtlearn.Record{
		{Type: fmtlearn.RecordCompress, Session: "s1", Arm: ArmRaw, RawPercent: 50, TokensSaved: 40, TS: start},
		{Type: fmtlearn.RecordCompress, Session: "s1", Arm: ArmRaw, RawPercent: 50, TokensSaved: 60, TS: start},
		{Type: fmtlearn.RecordCompress, Session: "s2", Arm: ArmFmt, RawPercent: 50, TokensSaved: 10, TS: start},
		// The share changed mid-session: in both arms, so left out.
		{Type: fmtlearn.RecordCompress, Session: "s3", Arm: ArmFmt, TS: start},
		{Type: fmtlearn.RecordCompress, Session: "s3", Arm: ArmRaw, TS: start},
		// Outside the experiment, and before the window.
		{Type: fmtlearn.RecordCompress, Command: "git", TS: start},
		{Type: fmtlearn.RecordCompress, Session: "old", Arm: ArmFmt, TS: start.Add(-48 * time.Hour)},
	}
	records := append(transcript("s1", start, 3), transcript("s2", start, 2)...)
	// A subagent of s2 counts for s2.
	records = append(records, transcript("s2/agent1", start, 1)...)
	records = append(records, transcript("unrelated", start, 4)...)

	got, mixed := SessionOutcomes(runs, records, map[string]string{"s1": "achieved"}, start.Add(-time.Hour))
	if mixed != 1 || len(got) != 2 {
		t.Fatalf("mixed %d, sessions %+v", mixed, got)
	}
	s1, s2 := got[0], got[1]
	if s1.Session != "s1" || s1.Arm != ArmRaw || s1.Runs != 2 || s1.TokensSaved != 100 || s1.Outcome != "achieved" {
		t.Errorf("s1: %+v", s1)
	}
	if s1.Instructions != 3 || s1.Reworked != 1 || s1.FirstTry != 2 || s1.TokensPerInstruction != 3000 {
		t.Errorf("s1 units: %+v", s1)
	}
	// Bash go test 0, then go test 1 twice: one repeat among 6 calls.
	if s1.ToolCalls != 6 || s1.RepeatCalls != 1 {
		t.Errorf("s1 calls: %+v", s1)
	}
	if s2.Arm != ArmFmt || s2.Instructions != 3 || s2.Outcome != "" {
		t.Errorf("s2: %+v", s2)
	}
}

func TestExperimentReadsTheIndex(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	records := make([]agentdx.Record, 0, 10*30)
	for i := range 10 {
		id := fmt.Sprintf("session-%d", i)
		a := ExperimentArm(id, 50)
		if err := fmtindex.Append(dir, fmtlearn.Record{Type: fmtlearn.RecordCompress, Session: id, Arm: a, RawPercent: 50, TS: now}); err != nil {
			t.Fatal(err)
		}
		records = append(records, transcript(id, now, 6)...)
	}
	r, err := Experiment(50, ExperimentOptions{
		RecoverDir: dir, Since: now.Add(-time.Hour),
		Transcripts: func(time.Time) ([]agentdx.Record, error) { return records, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Fmt.Sessions+r.Raw.Sessions != 10 || r.Fmt.Instructions+r.Raw.Instructions != 60 || r.RawPercent != 50 {
		t.Errorf("report: %+v", r)
	}
}

func TestExperimentOutcomesResolvesPerExecution(t *testing.T) {
	ev := func(exec string, r eventschema.OutcomeResult, a eventschema.OutcomeAssessment) *eventschema.Envelope {
		return &eventschema.Envelope{
			Type: eventschema.EventTypeOutcome, Association: eventschema.Association{Execution: exec},
			Payload: &eventschema.OutcomeEvent{Result: r, Assessment: a},
		}
	}
	got := ExperimentOutcomes([]*eventschema.Envelope{
		ev("s1", eventschema.OutcomeNotAchieved, eventschema.OutcomeVerification),
		ev("s1", eventschema.OutcomeAchieved, eventschema.OutcomeHuman),
		ev("s2", eventschema.OutcomeUnknown, eventschema.OutcomeHuman),
		ev("", eventschema.OutcomeAchieved, eventschema.OutcomeHuman),
	})
	if len(got) != 1 || got["s1"] != "achieved" {
		t.Errorf("outcomes: %v", got)
	}
}

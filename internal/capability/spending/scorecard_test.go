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
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard"
)

// writeTranscript writes one Claude Code session under root.
func writeTranscript(t *testing.T, root, session string, lines []string) {
	t.Helper()
	path := filepath.Join(root, "proj", session+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func userTurn(session string, at time.Time, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":%q,"message":{"content":%q}}`,
		at.Format(time.RFC3339), session, text)
}

// Every surface gets the agent KPIs, the MCP tool and /api/scorecard
// included: the CLI used to be the only one that added them.
func TestScorecardGradesAgentKPIsFromTranscripts(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	at := now.Add(-time.Hour)
	writeTranscript(t, root, "human", []string{
		userTurn("human", at, "refactor the retry path to use the shared backoff"),
		userTurn("human", at.Add(time.Minute), "ok"),
		userTurn("human", at.Add(2*time.Minute), "add a test for the jitter bound"),
		userTurn("human", at.Add(3*time.Minute), "now document the new option"),
	})
	// A /loop session: its pacing prompts are not acknowledgements.
	loop := make([]string, 0, 8)
	for i := range 8 {
		loop = append(loop, userTurn("loop", at.Add(time.Duration(i)*time.Minute), "continue"))
	}
	writeTranscript(t, root, "loop", loop)
	// Outside the window: never counted.
	writeTranscript(t, root, "old", []string{userTurn("old", now.AddDate(0, 0, -30), "ok")})

	s := Scorecard(context.Background(), nil, ScorecardParams{TranscriptRoot: root, Now: now})
	if s.OverallGrade == scorecard.GradeWarmingUp {
		t.Fatal("agent KPIs alone are a verdict, not warming up")
	}
	if got := s.ConfirmationGateRate.Value; got != 25 {
		t.Errorf("confirmation gate rate %.1f%%, want 25%% (1 ack in 4 human prompts)", got)
	}
}

func TestScorecardWarmsUpWithNothingToMeasure(t *testing.T) {
	s := Scorecard(context.Background(), nil, ScorecardParams{TranscriptRoot: t.TempDir()})
	if s.OverallGrade != scorecard.GradeWarmingUp {
		t.Errorf("grade %q with no store and no transcripts, want warming up", s.OverallGrade)
	}
}

func TestWithoutLoopSentinelsKeepsOccasionalAcks(t *testing.T) {
	in := make([]prompts.UserPrompt, 0, 2*loopSentinelRepeats+1)
	for range loopSentinelRepeats {
		in = append(in, prompts.UserPrompt{SessionID: "a", Text: " Continue "})
	}
	for range loopSentinelRepeats + 1 {
		in = append(in, prompts.UserPrompt{SessionID: "b", Text: "continue"})
	}
	if got := len(withoutLoopSentinels(in)); got != loopSentinelRepeats {
		t.Errorf("kept %d prompts, want the %d from the session under the threshold", got, loopSentinelRepeats)
	}
}

// The agent KPIs are set against the operator's own four weeks before
// the period, read from the same transcripts.
func TestScorecardComparesAgentKPIsWithOwnBaseline(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	at, earlier := now.Add(-time.Hour), now.AddDate(0, 0, -10)
	writeTranscript(t, root, "now", []string{
		userTurn("now", at, "refactor the retry path to use the shared backoff"),
		userTurn("now", at.Add(time.Minute), "ok"),
		userTurn("now", at.Add(2*time.Minute), "add a test for the jitter bound"),
		userTurn("now", at.Add(3*time.Minute), "now document the new option"),
	})
	writeTranscript(t, root, "before", []string{
		userTurn("before", earlier, "split the parser into two passes"),
		userTurn("before", earlier.Add(time.Minute), "yes"),
		userTurn("before", earlier.Add(2*time.Minute), "ok"),
		userTurn("before", earlier.Add(3*time.Minute), "add the golden files"),
	})
	s := Scorecard(context.Background(), nil, ScorecardParams{TranscriptRoot: root, Now: now})
	if got := s.ConfirmationGateRate.Value; got != 25 {
		t.Fatalf("CGR %.1f%%, want 25%% (the period alone)", got)
	}
	c := s.ConfirmationGateRate.VsBaseline
	if c == nil || c.Baseline != 50 || c.Direction != scorecard.DirectionBetter {
		t.Fatalf("CGR vs baseline = %+v, want 50%% and better", c)
	}
	if s.SelfBaseline == nil || !s.SelfBaseline.Until.Equal(now.AddDate(0, 0, -7)) {
		t.Errorf("self baseline = %+v", s.SelfBaseline)
	}
}

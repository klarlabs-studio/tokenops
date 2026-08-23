package plans

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type staticReader []*eventschema.Envelope

func (s staticReader) ReadEvents(_ context.Context, _ eventschema.EventType, _ time.Time) ([]*eventschema.Envelope, error) {
	return s, nil
}

func turnEnv(ts time.Time, startsMessage bool, tokens int64) *eventschema.Envelope {
	return &eventschema.Envelope{
		Type:      eventschema.EventTypePrompt,
		Timestamp: ts,
		Source:    "claude-code-jsonl",
		Attributes: map[string]string{
			"granularity":         "assistant_turn",
			"starts_user_message": map[bool]string{true: "true", false: "false"}[startsMessage],
		},
		Payload: &eventschema.PromptEvent{
			Provider:    eventschema.ProviderAnthropic,
			CostSource:  eventschema.CostSourcePlanIncluded,
			TotalTokens: tokens,
		},
	}
}

// The whole point of the plan window meter: a per-turn stream must still
// yield the vendor's message count. Before the starts_user_message flag
// every assistant_turn event was excluded, so the meter read 0/200 no
// matter how much traffic flowed.
func TestWindowCountsUserMessagesFromTurnStream(t *testing.T) {
	now := time.Now().UTC()
	envs := staticReader{
		turnEnv(now.Add(-time.Hour), true, 100),
		turnEnv(now.Add(-59*time.Minute), false, 100),
		turnEnv(now.Add(-58*time.Minute), false, 100),
		turnEnv(now.Add(-30*time.Minute), true, 100),
		turnEnv(now.Add(-29*time.Minute), false, 100),
	}
	got, err := ConsumptionInWindow(context.Background(), envs, "anthropic", now, 5*time.Hour)
	if err != nil {
		t.Fatalf("consumption: %v", err)
	}
	if got.MessagesInWindow != 2 {
		t.Errorf("MessagesInWindow = %d, want 2 (one per operator prompt, not per turn)",
			got.MessagesInWindow)
	}
	if got.TokensInWindow != 500 {
		t.Errorf("TokensInWindow = %d, want 500 (every turn's tokens still count)",
			got.TokensInWindow)
	}
}

// Sources that cannot identify prompt boundaries keep the old
// conservative behaviour rather than inflating the meter.
func TestUnflaggedTurnStreamStillExcluded(t *testing.T) {
	now := time.Now().UTC()
	env := turnEnv(now.Add(-time.Minute), false, 10)
	delete(env.Attributes, "starts_user_message")
	got, err := ConsumptionInWindow(context.Background(), staticReader{env}, "anthropic", now, 5*time.Hour)
	if err != nil {
		t.Fatalf("consumption: %v", err)
	}
	if got.MessagesInWindow != 0 {
		t.Errorf("MessagesInWindow = %d, want 0 for an unflagged assistant_turn source", got.MessagesInWindow)
	}
	if got.TokensInWindow != 10 {
		t.Errorf("TokensInWindow = %d, want 10", got.TokensInWindow)
	}
}

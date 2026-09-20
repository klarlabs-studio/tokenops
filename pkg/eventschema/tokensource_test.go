package eventschema_test

import (
	"encoding/json"
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A proxy with no tokenizer wired ships events with zero token counts by
// documented design. Nothing downstream can tell those apart from a
// request that genuinely used no tokens, so a deployment that forgot the
// tokenizer reports "0 tokens" with the same confidence as one that
// counted them.
//
// TokenSource is the same shape as CostSource: it says how the numbers
// beside it came to be, without changing their type.
func TestTokenSourceDefaultsToCounted(t *testing.T) {
	var p eventschema.PromptEvent
	if got := p.TokenProvenance(); got != eventschema.TokenSourceCounted {
		t.Errorf("empty TokenSource = %q, want %q (prior events round-trip unchanged)",
			got, eventschema.TokenSourceCounted)
	}
}

// The point of the field: an event whose counts nobody produced says so.
func TestTokenSourceDistinguishesUncounted(t *testing.T) {
	p := eventschema.PromptEvent{TokenSource: eventschema.TokenSourceUncounted}
	if p.TokensCounted() {
		t.Error("an uncounted event claims its tokens were counted")
	}
	counted := eventschema.PromptEvent{TokenSource: eventschema.TokenSourceCounted}
	if !counted.TokensCounted() {
		t.Error("a counted event claims otherwise")
	}
	vendor := eventschema.PromptEvent{TokenSource: eventschema.TokenSourceVendorReported}
	if !vendor.TokensCounted() {
		t.Error("vendor-reported counts are real counts")
	}
}

// Events written before this field existed must deserialise unchanged,
// as counted — that is what they were.
func TestPriorEventsRoundTripAsCounted(t *testing.T) {
	const prior = `{"prompt_hash":"h","provider":"openai","request_model":"gpt-4o",
		"input_tokens":10,"output_tokens":20,"total_tokens":30,"context_size":0}`
	var p eventschema.PromptEvent
	if err := json.Unmarshal([]byte(prior), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !p.TokensCounted() {
		t.Error("an event from before the field was added reads as uncounted")
	}
	if p.InputTokens != 10 || p.TotalTokens != 30 {
		t.Errorf("counts changed: %+v", p)
	}
}

// The field is omitted when it carries no information, so existing
// serialised output is byte-identical for the common case.
func TestCountedTokenSourceIsOmittedFromJSON(t *testing.T) {
	body, err := json.Marshal(eventschema.PromptEvent{InputTokens: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := back["token_source"]; present {
		t.Errorf("the default was serialised: %s", body)
	}
}

func TestUncountedTokenSourceSurvivesJSON(t *testing.T) {
	body, err := json.Marshal(eventschema.PromptEvent{
		TokenSource: eventschema.TokenSourceUncounted,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back eventschema.PromptEvent
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.TokensCounted() {
		t.Errorf("uncounted did not survive the round trip: %s", body)
	}
}

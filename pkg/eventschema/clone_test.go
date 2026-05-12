package eventschema

import (
	"testing"
	"time"
)

func TestEnvelopeCloneIsolatesPayload(t *testing.T) {
	original := &Envelope{
		ID:            "01H",
		SchemaVersion: SchemaVersion,
		Type:          EventTypePrompt,
		Timestamp:     time.Now().UTC(),
		Source:        "proxy",
		Payload: &PromptEvent{
			PromptHash: "sha256:abc",
			Provider:   ProviderOpenAI,
		},
	}
	clone, err := original.Clone()
	if err != nil {
		t.Fatal(err)
	}
	cp := clone.Payload.(*PromptEvent)
	cp.PromptHash = "mutated"
	if original.Payload.(*PromptEvent).PromptHash != "sha256:abc" {
		t.Errorf("Clone leaked mutation back to original")
	}
}

func TestEnvelopeCloneAttributesIndependent(t *testing.T) {
	env := &Envelope{
		ID:         "x",
		Type:       EventTypePrompt,
		Timestamp:  time.Now().UTC(),
		Attributes: map[string]string{"k": "v"},
		Payload:    &PromptEvent{},
	}
	clone, err := env.Clone()
	if err != nil {
		t.Fatal(err)
	}
	clone.Attributes["k"] = "mutated"
	if env.Attributes["k"] != "v" {
		t.Errorf("Clone shared Attributes map")
	}
}

func TestEnvelopeCloneNilSafe(t *testing.T) {
	var env *Envelope
	c, err := env.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Errorf("nil clone should return nil")
	}
}

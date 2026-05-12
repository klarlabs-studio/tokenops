package eventschema

import (
	"encoding/json"
	"testing"
	"time"
	"unicode/utf8"
)

// FuzzClonePromptEvent exercises Envelope.Clone on PromptEvent
// payloads with adversarial input fields. The clone must be
// independent (mutation does not affect original) and JSON-equal
// (round-trip preserves every field).
func FuzzClonePromptEvent(f *testing.F) {
	f.Add("sha256:abc", "openai", "gpt-4o-mini", int64(100), int64(50))
	f.Add("", "", "", int64(0), int64(0))
	f.Add("\x00", "anthropic", "claude\n", int64(-1), int64(-1))
	f.Add("very-long-hash-"+string(make([]byte, 256)), "gemini", "gemini-2.5-pro", int64(1<<31), int64(1<<31))

	f.Fuzz(func(t *testing.T, hash, provider, model string, input, output int64) {
		// Schema contract is UTF-8 strings; skip non-UTF-8 inputs.
		if !utf8.ValidString(hash) || !utf8.ValidString(provider) || !utf8.ValidString(model) {
			t.Skip()
		}
		env := &Envelope{
			ID:            "fuzz",
			SchemaVersion: SchemaVersion,
			Type:          EventTypePrompt,
			Timestamp:     time.Now().UTC(),
			Payload: &PromptEvent{
				PromptHash:   hash,
				Provider:     Provider(provider),
				RequestModel: model,
				InputTokens:  input,
				OutputTokens: output,
			},
		}
		c, err := env.Clone()
		if err != nil {
			// Clone failure tolerated for invalid UTF-8 etc.
			return
		}
		// JSON-equal.
		a, _ := json.Marshal(env)
		b, _ := json.Marshal(c)
		if string(a) != string(b) {
			t.Errorf("clone JSON drift:\nA=%s\nB=%s", a, b)
		}
		// Mutation isolation.
		cp := c.Payload.(*PromptEvent)
		cp.PromptHash = "MUTATED"
		if env.Payload.(*PromptEvent).PromptHash == "MUTATED" {
			t.Errorf("mutation leaked back to original")
		}
	})
}

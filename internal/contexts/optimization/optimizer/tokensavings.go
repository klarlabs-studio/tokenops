package optimizer

import (
	"encoding/json"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EstimateTokenSavings returns tokens(before) - tokens(after) measured over
// the request's actual message content, or a byte/4 fallback when no
// tokenizer is available. It replaces the earlier "canary" estimate
// (tokenizing strings.Repeat("a ", n)), which returned essentially the same
// number as the fallback and systematically under-counted dense payloads
// (code, JSON) because spaced single letters tokenize far lighter than the
// bytes actually removed. Only OpenAI/Anthropic chat shapes are extracted —
// the optimizers that call this restrict themselves to those providers.
//
// Prefer EstimateTokenSavingsValue. This form cannot distinguish a
// counted no-saving from a failure to count, and returns 0 for both.
func EstimateTokenSavings(tk *tokenizer.Registry, provider eventschema.Provider, before, after []byte, fallbackBytes int) int64 {
	return int64(EstimateTokenSavingsValue(tk, provider, before, after, fallbackBytes).AmountOr(0))
}

// EstimateTokenSavingsValue is EstimateTokenSavings with its provenance
// attached.
//
// The int64 form collapses three different outcomes into one number:
//
//   - the tokenizer counted and found a saving — measured;
//   - the tokenizer counted and found none — measured zero, a real
//     answer;
//   - nothing could count, so bytes/4 stood in — an estimate, and where
//     there were no bytes to divide, not an answer at all.
//
// The third is the one that matters. It returned 0, which reads exactly
// like the second, so an optimizer that could not measure its own effect
// reported that it had no effect — with the same confidence as one that
// had measured it. Savings feed the scorecard, the routing proposals and
// the coach, all of which act on the figure.
func EstimateTokenSavingsValue(tk *tokenizer.Registry, provider eventschema.Provider, before, after []byte, fallbackBytes int) measurement.Value {
	if tk != nil {
		bn, errB := tk.CountText(provider, MessageContentText(before))
		an, errA := tk.CountText(provider, MessageContentText(after))
		if errB == nil && errA == nil {
			d := bn - an
			if d < 0 {
				d = 0
			}
			return measurement.Measured(float64(d), "tokenizer")
		}
	}
	// No tokenizer, or it refused this provider. bytes/4 is a rule of
	// thumb, not a count, and it needs bytes to work on: without them
	// there is nothing to report rather than a saving of zero.
	if fallbackBytes <= 0 {
		return measurement.Unknown(
			"no tokenizer for this provider and no byte delta to fall back on, " +
				"so the saving was not counted")
	}
	return measurement.Estimated(float64(fallbackBytes/4), "byte_delta",
		"bytes/4 heuristic; no tokenizer counted this")
}

// MessageContentText concatenates the text content of a chat request body's
// messages (plus a top-level Anthropic system prompt), so a tokenizer can
// count what the model actually sees. Non-text parts (images, tool calls)
// are skipped. Returns "" when the body isn't a recognised chat shape.
func MessageContentText(body []byte) string {
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return ""
	}
	var b strings.Builder
	if sys, ok := raw["system"]; ok {
		appendContentText(&b, sys)
	}
	if msgs, ok := raw["messages"]; ok {
		var arr []map[string]json.RawMessage
		if json.Unmarshal(msgs, &arr) == nil {
			for _, m := range arr {
				if c, ok := m["content"]; ok {
					appendContentText(&b, c)
				}
			}
		}
	}
	return b.String()
}

// appendContentText handles both string content and an array-of-parts
// content (each part carrying a "text" field).
func appendContentText(b *strings.Builder, raw json.RawMessage) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		b.WriteString(s)
		b.WriteByte('\n')
		return
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) == nil {
		for _, p := range parts {
			t, ok := p["text"]
			if !ok {
				continue
			}
			var text string
			if json.Unmarshal(t, &text) == nil {
				b.WriteString(text)
				b.WriteByte('\n')
			}
		}
	}
}

package optimizer

import (
	"encoding/json"
	"strings"

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
func EstimateTokenSavings(tk *tokenizer.Registry, provider eventschema.Provider, before, after []byte, fallbackBytes int) int64 {
	if tk != nil {
		bn, errB := tk.CountText(provider, MessageContentText(before))
		an, errA := tk.CountText(provider, MessageContentText(after))
		if errB == nil && errA == nil {
			if d := bn - an; d > 0 {
				return int64(d)
			}
			return 0
		}
	}
	if fallbackBytes < 0 {
		fallbackBytes = 0
	}
	return int64(fallbackBytes / 4)
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

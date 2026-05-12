package tokenizer

import (
	"encoding/json"
	"fmt"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// ExtractMessages parses a request body and returns the chat messages in
// the provider's schema. Unknown shapes return an empty slice (not an
// error); callers should fall back to a raw-text count when extraction
// returns nothing.
func ExtractMessages(provider eventschema.Provider, body []byte) ([]Message, error) {
	if len(body) == 0 {
		return nil, nil
	}
	switch provider {
	case eventschema.ProviderOpenAI:
		return extractOpenAI(body)
	case eventschema.ProviderAnthropic:
		return extractAnthropic(body)
	case eventschema.ProviderGemini:
		return extractGemini(body)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, provider)
	}
}

// openaiMessage / openaiBody mirror the relevant subset of /v1/chat/completions.
type openaiBody struct {
	Messages []openaiMessage `json:"messages"`
}

type openaiMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func extractOpenAI(body []byte) ([]Message, error) {
	var b openaiBody
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, fmt.Errorf("openai body: %w", err)
	}
	out := make([]Message, 0, len(b.Messages))
	for _, m := range b.Messages {
		out = append(out, Message{Role: m.Role, Content: flattenContent(m.Content)})
	}
	return out, nil
}

// anthropicBody mirrors the relevant subset of /v1/messages. Note: the
// system prompt sits at the body root, not inside Messages.
type anthropicBody struct {
	System   json.RawMessage    `json:"system"`
	Messages []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func extractAnthropic(body []byte) ([]Message, error) {
	var b anthropicBody
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, fmt.Errorf("anthropic body: %w", err)
	}
	out := make([]Message, 0, len(b.Messages)+1)
	if sys := flattenContent(b.System); sys != "" {
		out = append(out, Message{Role: "system", Content: sys})
	}
	for _, m := range b.Messages {
		out = append(out, Message{Role: m.Role, Content: flattenContent(m.Content)})
	}
	return out, nil
}

// geminiBody mirrors the relevant subset of /v1/models/.../generateContent.
type geminiBody struct {
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

func extractGemini(body []byte) ([]Message, error) {
	var b geminiBody
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, fmt.Errorf("gemini body: %w", err)
	}
	var out []Message
	if b.SystemInstruction != nil {
		out = append(out, Message{
			Role:    "system",
			Content: joinParts(b.SystemInstruction.Parts),
		})
	}
	for _, c := range b.Contents {
		out = append(out, Message{Role: c.Role, Content: joinParts(c.Parts)})
	}
	return out, nil
}

func joinParts(parts []geminiPart) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0].Text
	}
	var size int
	for _, p := range parts {
		size += len(p.Text)
	}
	out := make([]byte, 0, size+len(parts))
	for i, p := range parts {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, p.Text...)
	}
	return string(out)
}

// flattenContent collapses an OpenAI/Anthropic content field into plain
// text. Both providers accept either a string or an array of typed parts
// (text, image, tool_use, ...). Non-text parts are ignored — multimodal
// counts are out of scope for the heuristic tokenizer.
func flattenContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// Fast path: plain string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Array of parts.
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var size int
		for _, p := range parts {
			size += len(p.Text)
		}
		out := make([]byte, 0, size+len(parts))
		for i, p := range parts {
			if p.Type != "" && p.Type != "text" {
				continue
			}
			if i > 0 {
				out = append(out, '\n')
			}
			out = append(out, p.Text...)
		}
		return string(out)
	}
	// Object with text field (Anthropic system can be wrapped this way).
	var obj struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Text
	}
	return ""
}

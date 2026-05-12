package tokenizer

import (
	"math"
	"unicode/utf8"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// heuristic is the shared core of the per-provider tokenizers. It uses a
// configurable bytes-per-token ratio, with separate ratios for ASCII and
// non-ASCII (since CJK / emoji typically tokenise far denser per char in
// every modern BPE/SentencePiece scheme).
type heuristic struct {
	provider           eventschema.Provider
	asciiCharsPerToken float64
	otherCharsPerToken float64
	perMessageOverhead int
	wrapperOverhead    int
}

func (h heuristic) Provider() eventschema.Provider { return h.provider }

func (h heuristic) CountText(s string) int {
	if s == "" {
		return 0
	}
	var ascii, other int
	for _, r := range s {
		if r < utf8.RuneSelf {
			ascii++
		} else {
			other++
		}
	}
	tokens := float64(ascii)/h.asciiCharsPerToken + float64(other)/h.otherCharsPerToken
	return int(math.Ceil(tokens))
}

func (h heuristic) CountMessages(msgs []Message) int {
	if len(msgs) == 0 {
		return 0
	}
	total := h.wrapperOverhead
	for _, m := range msgs {
		// Per-message overhead represents the role/turn marker tokens that
		// the provider's chat template inserts. Add the role text too —
		// it's content the provider will tokenise as well.
		total += h.perMessageOverhead
		if m.Role != "" {
			total += h.CountText(m.Role)
		}
		total += h.CountText(m.Content)
	}
	return total
}

// NewOpenAITokenizer returns a heuristic OpenAI tokenizer. The default
// ratios are tuned to cl100k_base / o200k_base behaviour on English
// prose; production deployments that need exact pre-flight accuracy
// should swap in a tiktoken-backed Tokenizer via Registry.Set.
func NewOpenAITokenizer() Tokenizer {
	return heuristic{
		provider:           eventschema.ProviderOpenAI,
		asciiCharsPerToken: 4.0, // ~25% tokens-per-char for English
		otherCharsPerToken: 1.5, // CJK/emoji often 1–2 chars per token
		perMessageOverhead: 4,   // ChatML role/turn markers
		wrapperOverhead:    3,   // Reply primer
	}
}

// NewAnthropicTokenizer returns a heuristic Anthropic tokenizer. Anthropic
// does not publish their tokenizer; the ratios match the published
// "approximately 3.5 chars per token" guidance for Claude 3+.
func NewAnthropicTokenizer() Tokenizer {
	return heuristic{
		provider:           eventschema.ProviderAnthropic,
		asciiCharsPerToken: 3.5,
		otherCharsPerToken: 1.4,
		perMessageOverhead: 5, // Human:/Assistant: prefixes + newlines
		wrapperOverhead:    3,
	}
}

// NewGeminiTokenizer returns a heuristic Gemini tokenizer. Gemini uses
// SentencePiece; the published guidance is roughly 4 chars per token for
// Latin scripts.
func NewGeminiTokenizer() Tokenizer {
	return heuristic{
		provider:           eventschema.ProviderGemini,
		asciiCharsPerToken: 4.0,
		otherCharsPerToken: 1.6,
		perMessageOverhead: 3,
		wrapperOverhead:    2,
	}
}

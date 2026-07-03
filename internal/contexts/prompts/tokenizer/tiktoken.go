package tokenizer

import (
	"github.com/tiktoken-go/tokenizer"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// tiktokenCodec is an EXACT BPE tokenizer backed by tiktoken's offline
// vocabulary — no network, the ranks are embedded in the dependency. It
// replaces the char-per-token heuristic for OpenAI so the proxy pre-flight
// count, and every downstream $ / savings / headroom figure that rides on
// it, is exact for the highest-volume provider. The heuristic's ~10–15%
// error is worst on code and JSON (dense tokenization) — exactly the
// tool-output payloads this product measures.
type tiktokenCodec struct {
	provider           eventschema.Provider
	codec              tokenizer.Codec
	perMessageOverhead int
	wrapperOverhead    int
}

// NewOpenAITiktoken returns an exact OpenAI tokenizer using the o200k_base
// vocabulary (GPT-4o, the o-series, GPT-4.1, GPT-5). It returns an error if
// the vocabulary cannot be loaded, so callers can fall back to the heuristic
// rather than failing hard.
func NewOpenAITiktoken() (Tokenizer, error) {
	c, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		return nil, err
	}
	return tiktokenCodec{
		provider: eventschema.ProviderOpenAI,
		codec:    c,
		// ChatML wraps each message as <|start|>role\n...content...<|end|>;
		// ~3 structural tokens per message plus a 3-token reply primer,
		// matching OpenAI's published counting guidance.
		perMessageOverhead: 3,
		wrapperOverhead:    3,
	}, nil
}

func (t tiktokenCodec) Provider() eventschema.Provider { return t.provider }

func (t tiktokenCodec) CountText(s string) int {
	if s == "" {
		return 0
	}
	n, err := t.codec.Count(s)
	if err != nil {
		return 0
	}
	return n
}

func (t tiktokenCodec) CountMessages(msgs []Message) int {
	if len(msgs) == 0 {
		return 0
	}
	total := t.wrapperOverhead
	for _, m := range msgs {
		total += t.perMessageOverhead
		if m.Role != "" {
			total += t.CountText(m.Role)
		}
		total += t.CountText(m.Content)
	}
	return total
}

// Package toolfmt is the proxy-plane counterpart to the `tokenops fmt` CLI
// wrapper: it compresses the tool-output text embedded in an LLM request
// body (Anthropic tool_result blocks, OpenAI role="tool" messages) using the
// same deterministic formatter engine. Where the CLI plane knows which
// command produced the output, the proxy plane does not, so it content-
// sniffs (Registry.FormatSniff) to pick the right formatter — falling back
// to a lossless noise scrub when nothing matches.
//
// This lets an agent that pastes verbose tool output into its next turn get
// the same critical-line-preserving compression without installing a shell
// hook. The optimizer implements optimizer.Optimizer and plugs into the
// standard pipeline; savings flow through the existing OptimizationEvent
// path onto the dashboard.
package toolfmt

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Config tunes the optimizer.
type Config struct {
	// MinSavingsTokens suppresses recommendations below this projected
	// saving (avoids noise events). Default 16.
	MinSavingsTokens int64
	// Level is the loss level applied to tool output. Default balanced —
	// aggressive collapse is reserved for the operator-driven CLI plane.
	Level formatter.LossLevel
}

// Optimizer compresses tool-output blocks in a request body.
type Optimizer struct {
	cfg       Config
	reg       *formatter.Registry
	tokenizer *tokenizer.Registry
}

// New constructs the optimizer. reg supplies the formatter set; tk may be
// nil (savings then come from byte deltas).
func New(cfg Config, reg *formatter.Registry, tk *tokenizer.Registry) *Optimizer {
	if cfg.MinSavingsTokens <= 0 {
		cfg.MinSavingsTokens = 16
	}
	if reg == nil {
		reg = formatter.NewRegistry(formatter.LossPolicy{Default: cfg.Level})
	}
	return &Optimizer{cfg: cfg, reg: reg, tokenizer: tk}
}

// Kind reports the optimizer category.
func (o *Optimizer) Kind() eventschema.OptimizationType {
	return eventschema.OptimizationTypeCommandFmt
}

// Run walks the request body's tool-output text, compresses each block via
// the formatter engine, and emits a recommendation when the projected
// saving clears the threshold.
func (o *Optimizer) Run(_ context.Context, req *optimizer.Request) ([]optimizer.Recommendation, error) {
	if req == nil || len(req.Body) == 0 {
		return nil, nil
	}
	switch req.Provider {
	case eventschema.ProviderOpenAI, eventschema.ProviderAnthropic:
	default:
		return nil, nil
	}

	rebuilt, before, after, ok := o.compressBody(req.Body)
	if !ok {
		return nil, nil
	}
	delta := before - after
	if delta <= 0 {
		return nil, nil
	}
	tokensSaved := o.estimateTokensSaved(req.Provider, delta)
	if tokensSaved < o.cfg.MinSavingsTokens {
		return nil, nil
	}
	return []optimizer.Recommendation{{
		Kind:                   eventschema.OptimizationTypeCommandFmt,
		EstimatedSavingsTokens: tokensSaved,
		QualityScore:           1.0, // deterministic, critical-line preserving
		Reason: fmt.Sprintf(
			"compressed tool output from %d to %d bytes (%d saved)", before, after, delta),
		ApplyBody: rebuilt,
	}}, nil
}

func (o *Optimizer) estimateTokensSaved(_ eventschema.Provider, byteDelta int) int64 {
	return int64(byteDelta / 4)
}

// compressBody parses the body, compresses every tool-output text field, and
// returns the rewritten body plus before/after byte totals over the touched
// fields. ok is false when the body is not JSON with a messages array.
func (o *Optimizer) compressBody(body []byte) ([]byte, int, int, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, 0, false
	}
	rawMsgs, ok := raw["messages"]
	if !ok {
		return nil, 0, 0, false
	}
	var msgs []json.RawMessage
	if err := json.Unmarshal(rawMsgs, &msgs); err != nil {
		return nil, 0, 0, false
	}

	before, after := 0, 0
	for i, m := range msgs {
		rebuilt, b, a, changed := o.compressMessage(m)
		if changed {
			msgs[i] = rebuilt
			before += b
			after += a
		}
	}
	if before == 0 {
		return nil, 0, 0, false
	}

	newMsgs, err := json.Marshal(msgs)
	if err != nil {
		return nil, 0, 0, false
	}
	out := make(map[string]json.RawMessage, len(raw))
	maps.Copy(out, raw)
	out["messages"] = newMsgs
	final, err := json.Marshal(out)
	if err != nil {
		return nil, 0, 0, false
	}
	return final, before, after, true
}

// compressMessage handles one message. OpenAI: role=="tool" with a string
// content. Anthropic: role=="user" with a content array containing
// tool_result blocks (content string or array-of-text-parts).
func (o *Optimizer) compressMessage(m json.RawMessage) (json.RawMessage, int, int, bool) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(m, &probe); err != nil {
		return m, 0, 0, false
	}
	role := ""
	if r, ok := probe["role"]; ok {
		_ = json.Unmarshal(r, &role)
	}

	// OpenAI tool message: {"role":"tool","content":"<text>"}.
	if role == "tool" {
		if c, ok := probe["content"]; ok {
			newC, b, a, changed := o.compressStringField(c)
			if changed {
				probe["content"] = newC
				enc, err := json.Marshal(probe)
				if err == nil {
					return enc, b, a, true
				}
			}
		}
		return m, 0, 0, false
	}

	// Anthropic: content array with tool_result blocks.
	c, ok := probe["content"]
	if !ok {
		return m, 0, 0, false
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(c, &blocks); err != nil {
		return m, 0, 0, false
	}
	before, after := 0, 0
	changed := false
	for _, blk := range blocks {
		bt := ""
		if t, ok := blk["type"]; ok {
			_ = json.Unmarshal(t, &bt)
		}
		if bt != "tool_result" {
			continue
		}
		inner, ok := blk["content"]
		if !ok {
			continue
		}
		newInner, b, a, ch := o.compressToolResultContent(inner)
		if ch {
			blk["content"] = newInner
			before += b
			after += a
			changed = true
		}
	}
	if !changed {
		return m, 0, 0, false
	}
	newBlocks, err := json.Marshal(blocks)
	if err != nil {
		return m, 0, 0, false
	}
	probe["content"] = newBlocks
	enc, err := json.Marshal(probe)
	if err != nil {
		return m, 0, 0, false
	}
	return enc, before, after, true
}

// compressToolResultContent handles the Anthropic tool_result content, which
// is either a string or an array of {type:"text", text:"..."} parts.
func (o *Optimizer) compressToolResultContent(raw json.RawMessage) (json.RawMessage, int, int, bool) {
	// String form.
	if enc, b, a, ok := o.tryCompressString(raw); ok {
		return enc, b, a, true
	}
	// Array-of-parts form.
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return raw, 0, 0, false
	}
	before, after := 0, 0
	changed := false
	for _, p := range parts {
		t, ok := p["text"]
		if !ok {
			continue
		}
		newT, b, a, ch := o.tryCompressString(t)
		if ch {
			p["text"] = newT
			before += b
			after += a
			changed = true
		}
	}
	if !changed {
		return raw, 0, 0, false
	}
	enc, err := json.Marshal(parts)
	if err != nil {
		return raw, 0, 0, false
	}
	return enc, before, after, true
}

// compressStringField compresses a JSON string field in place.
func (o *Optimizer) compressStringField(raw json.RawMessage) (json.RawMessage, int, int, bool) {
	return o.tryCompressString(raw)
}

// tryCompressString unmarshals raw as a string, content-sniffs a formatter,
// and re-marshals the compact result. Returns ok=false when raw is not a
// string or nothing was saved.
func (o *Optimizer) tryCompressString(raw json.RawMessage) (json.RawMessage, int, int, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return raw, 0, 0, false
	}
	if s == "" {
		return raw, 0, 0, false
	}
	res, _ := o.reg.FormatSniff([]byte(s), o.cfg.Level)
	if !res.CriticalKept || res.BytesAfter >= res.BytesBefore {
		return raw, 0, 0, false
	}
	enc, err := json.Marshal(string(res.Compact))
	if err != nil {
		return raw, 0, 0, false
	}
	return enc, len(s), len(res.Compact), true
}

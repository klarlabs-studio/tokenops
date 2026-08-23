// Package taskclass classifies a single agent turn as mechanical
// execution or reasoning work, so a routing rule can be scoped to the
// kind of work it is safe for.
//
// The signals are the ones the prompt coach already mines from real
// sessions — instruction length, tool-call density, and whether the
// operator just rejected the previous answer — read here from the
// request body so the call can be made in the request path rather than
// after the fact.
//
// The classifier abstains by design. Anything that is not clearly one
// kind or the other comes back Unknown, and callers leave the model
// alone. Routing a reasoning turn down to save tokens is exactly the
// trade an operator does not want made for them silently, so the cost
// of a wrong Mechanical call is much higher than the cost of a missed
// one.
package taskclass

import (
	"encoding/json"
	"strings"
)

// Class is the coarse kind of work a turn represents.
type Class string

// Known classes. Unknown means the classifier declined to call it.
const (
	Unknown    Class = "unknown"
	Mechanical Class = "mechanical"
	Reasoning  Class = "reasoning"
)

// Config tunes the thresholds. Zero values fall back to the defaults,
// which are deliberately conservative: they classify only the clear
// cases and leave the middle as Unknown.
type Config struct {
	// MaxMechanicalWords is the longest instruction still considered
	// mechanical. Default 5, which matches where the prompt coach's own
	// length distribution puts the bulk of directive prompts ("continue",
	// "proceed", "fix it"). Above it an instruction starts naming what to
	// do rather than just telling the agent to carry on, and the turn is
	// no longer clearly safe to route down.
	MaxMechanicalWords int
	// MinToolDensity is the share of messages that must be tool traffic
	// for a turn to read as execution. Default 0.5.
	MinToolDensity float64
	// MinReasoningWords is the instruction length above which a turn is
	// reasoning work regardless of tool traffic. Default 80.
	MinReasoningWords int
}

func (c Config) withDefaults() Config {
	if c.MaxMechanicalWords <= 0 {
		c.MaxMechanicalWords = 5
	}
	if c.MinToolDensity <= 0 {
		c.MinToolDensity = 0.5
	}
	if c.MinReasoningWords <= 0 {
		c.MinReasoningWords = 80
	}
	return c
}

// Signals is what the classifier observed, returned alongside the Class
// so a routing decision can be explained rather than asserted.
type Signals struct {
	Class Class
	// InstructionWords counts the words in the operator's most recent
	// instruction.
	InstructionWords int
	// ToolDensity is the share of messages that are tool_use or
	// tool_result parts.
	ToolDensity float64
	// Regenerate reports that the latest instruction rejects the prior
	// answer.
	Regenerate bool
	// Reason explains the call in one line.
	Reason string
}

// rejection markers that indicate the operator is asking for another
// attempt rather than a new task. Deliberately short and unambiguous —
// a false positive here only costs a missed routing opportunity.
var rejectionMarkers = []string{
	"no,", "nope", "wrong", "try again", "that's not", "thats not",
	"incorrect", "revert", "undo", "not what i", "still broken",
	"doesn't work", "doesnt work", "redo",
}

// Classify reads a provider chat body and reports the kind of work the
// turn represents. Bodies that are not recognisable chat shapes return
// Unknown.
func Classify(body []byte, cfg Config) Signals {
	cfg = cfg.withDefaults()
	msgs, ok := parseMessages(body)
	if !ok || len(msgs) == 0 {
		return Signals{Class: Unknown, Reason: "body is not a recognisable chat request"}
	}

	var toolParts, totalParts int
	lastInstruction := ""
	for _, m := range msgs {
		kinds, text := describeContent(m.Content)
		for _, k := range kinds {
			totalParts++
			if k == "tool_use" || k == "tool_result" {
				toolParts++
			}
		}
		// The operator's instructions are the user messages that are not
		// tool results.
		if m.Role == "user" && text != "" && !onlyToolResults(kinds) {
			lastInstruction = text
		}
	}
	if totalParts == 0 {
		return Signals{Class: Unknown, Reason: "no message content to inspect"}
	}

	sig := Signals{
		InstructionWords: len(strings.Fields(lastInstruction)),
		ToolDensity:      float64(toolParts) / float64(totalParts),
		Regenerate:       isRejection(lastInstruction),
	}

	switch {
	case sig.Regenerate:
		// The operator wants a better answer, not a cheaper one.
		sig.Class = Reasoning
		sig.Reason = "operator rejected the previous answer"
	case sig.InstructionWords >= cfg.MinReasoningWords:
		sig.Class = Reasoning
		sig.Reason = "long specifying instruction"
	case sig.InstructionWords > 0 &&
		sig.InstructionWords <= cfg.MaxMechanicalWords &&
		sig.ToolDensity >= cfg.MinToolDensity:
		sig.Class = Mechanical
		sig.Reason = "terse instruction over mostly tool traffic"
	default:
		sig.Class = Unknown
		sig.Reason = "neither clearly mechanical nor clearly reasoning"
	}
	return sig
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// parseMessages pulls the messages array out of an OpenAI/Anthropic
// chat body. A top-level system prompt is not a turn signal and is
// ignored.
func parseMessages(body []byte) ([]rawMessage, bool) {
	if len(body) == 0 {
		return nil, false
	}
	var envelope struct {
		Messages []rawMessage `json:"messages"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, false
	}
	if envelope.Messages == nil {
		return nil, false
	}
	return envelope.Messages, true
}

// describeContent returns the part kinds in a message's content plus its
// concatenated text. String content counts as a single "text" part.
func describeContent(raw json.RawMessage) (kinds []string, text string) {
	if len(raw) == 0 {
		return nil, ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return []string{"text"}, asString
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return nil, ""
	}
	var b strings.Builder
	for _, p := range parts {
		kind := p.Type
		if kind == "" {
			kind = "text"
		}
		kinds = append(kinds, kind)
		if p.Text != "" {
			b.WriteString(p.Text)
			b.WriteByte(' ')
		}
	}
	return kinds, strings.TrimSpace(b.String())
}

// onlyToolResults reports whether every part is a tool result — the
// shape Claude Code uses to feed output back to the model, which is not
// an operator instruction.
func onlyToolResults(kinds []string) bool {
	if len(kinds) == 0 {
		return false
	}
	for _, k := range kinds {
		if k != "tool_result" {
			return false
		}
	}
	return true
}

func isRejection(instruction string) bool {
	s := strings.ToLower(strings.TrimSpace(instruction))
	if s == "" {
		return false
	}
	for _, m := range rejectionMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

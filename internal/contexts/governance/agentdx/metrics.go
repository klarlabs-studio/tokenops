// Package agentdx measures what an agent session is like to work with,
// as opposed to what it costs.
//
// The wedge scorecard answers "how efficiently were tokens spent". These
// metrics answer a different question: how many turns did a request take,
// how often did the agent redo its own work, how often did the operator
// have to stop it. Cost metrics can look healthy while the experience is
// poor, and nothing in the product measured the second thing.
//
// Every metric here is derived from transcripts the client already writes.
// One deliberate omission: time-to-first-token. PromptEvent carries the
// field, but no passive reader can populate it — a transcript records when
// a turn finished, never when it started streaming — and inventing a value
// is the failure this package exists downstream of. TTFT stays proxy-only.
package agentdx

import (
	"math"
	"sort"
	"strings"
	"time"
)

// RecordKind classifies one transcript entry.
type RecordKind string

// Record kinds.
const (
	// KindPrompt is an instruction the operator typed. It opens a unit of
	// work: everything until the next prompt is the agent answering it.
	KindPrompt RecordKind = "prompt"
	// KindAssistantTurn is one model response.
	KindAssistantTurn RecordKind = "assistant_turn"
	// KindToolUse is one tool invocation.
	KindToolUse RecordKind = "tool_use"
	// KindInterrupt is the operator stopping the agent mid-flight.
	KindInterrupt RecordKind = "interrupt"
	// KindCompaction is a context compaction.
	KindCompaction RecordKind = "compaction"
)

// Record is one transcript entry, flattened to what the metrics need.
type Record struct {
	At        time.Time
	SessionID string
	Kind      RecordKind
	// ToolName is set for KindToolUse.
	ToolName string
	// FilePath is the file a tool acted on, when it acted on one.
	FilePath string
	// InputTokens is the context size at this turn, for KindAssistantTurn.
	InputTokens int64
	// Provider is which upstream served the turn, when the client records
	// it. Only opencode does — it is a multi-provider client, so the
	// question "which provider is this experience" is one only it can
	// answer.
	Provider string
	// Rejects marks a prompt in which the operator rejected what they had
	// just been given. It is the closest thing to a quality verdict a
	// transcript holds: they read the reply and said it was wrong.
	Rejects bool
}

// sortedByTime returns records in chronological order without mutating
// the caller's slice.
func sortedByTime(records []Record) []Record {
	out := make([]Record, len(records))
	copy(out, records)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Metrics is the agent-experience roll-up.
type Metrics struct {
	Sessions int `json:"sessions"`
	Prompts  int `json:"prompts"`
	// MedianTurnsPerPrompt is how many model turns a typical instruction
	// costs. The median rather than the mean: a handful of very long
	// units would otherwise hide what most requests feel like.
	MedianTurnsPerPrompt float64 `json:"median_turns_per_prompt"`
	// P90TurnsPerPrompt is the tail — the requests that drag.
	P90TurnsPerPrompt float64 `json:"p90_turns_per_prompt"`
	// ReworkRatePct is the share of edits that touch a file already
	// edited while answering the same instruction: the agent revising
	// itself rather than getting it right first.
	ReworkRatePct float64 `json:"rework_rate_pct"`
	// EscalationRatePct is the share of instructions that spawned a
	// subagent.
	EscalationRatePct float64 `json:"escalation_rate_pct"`
	// MedianContextGrowthTokens is the typical context increase between
	// consecutive turns — what eventually forces a compaction.
	MedianContextGrowthTokens int64 `json:"median_context_growth_tokens"`
	// CompactionsPerSession counts context compactions per session.
	CompactionsPerSession float64 `json:"compactions_per_session"`
	// InterruptRatePct is the share of instructions the operator had to
	// interrupt.
	InterruptRatePct float64 `json:"interrupt_rate_pct"`
	// MedianSecondsPerPrompt is the wall-clock a typical instruction
	// takes from being typed to the agent's last turn. Turns measure
	// effort; this measures waiting, and they diverge when turns are slow.
	MedianSecondsPerPrompt float64 `json:"median_seconds_per_prompt"`
	// P90SecondsPerPrompt is the waiting tail.
	P90SecondsPerPrompt float64 `json:"p90_seconds_per_prompt"`
	// MedianTokensPerPrompt is what a typical instruction costs in
	// context, summed across its turns.
	MedianTokensPerPrompt int64 `json:"median_tokens_per_prompt"`
	// MedianToolCallsPerPrompt is how much machinery a typical
	// instruction sets in motion.
	MedianToolCallsPerPrompt float64 `json:"median_tool_calls_per_prompt"`
	// FirstTryRatePct is the share of instructions the agent completed
	// without reworking a file, being interrupted, or delegating. The
	// closest single number to "it just worked".
	FirstTryRatePct float64 `json:"first_try_rate_pct"`
	// TotalEdits is the denominator behind ReworkRatePct, exposed so a
	// caller can tell "no rework" from "no edits".
	TotalEdits int `json:"total_edits"`
	// ByProvider breaks the same metrics down per upstream, for clients
	// that route to more than one. Empty when nothing recorded a
	// provider — which is every client except opencode.
	ByProvider map[string]Metrics `json:"by_provider,omitempty"`
}

// escalationTools are the tools that hand work to a subagent. Names are
// compared lower-cased: the clients spell them differently (Claude Code
// "Task", opencode "task") and a case-sensitive match silently reported
// zero delegation for every client but one.
var escalationTools = map[string]bool{"task": true, "agent": true}

// editTools are the tools that modify a file, for the rework signal.
var editTools = map[string]bool{
	"edit": true, "write": true, "notebookedit": true,
	"multiedit": true, "patch": true, "apply_patch": true,
}

// isEscalation and isEdit compare tool names without regard to case.
func isEscalation(name string) bool { return escalationTools[strings.ToLower(name)] }
func isEdit(name string) bool       { return editTools[strings.ToLower(name)] }

// ComputeByProvider rolls the records up overall and again per upstream.
//
// Only a multi-provider client records which upstream served a turn, so
// this is the only way to ask whether one provider is a worse experience
// than another — the same instruction, the same operator, a different
// model behind it.
func ComputeByProvider(records []Record) Metrics {
	overall := Compute(records)
	byProvider := map[string][]Record{}
	for _, r := range records {
		if r.Provider == "" {
			continue
		}
		byProvider[r.Provider] = append(byProvider[r.Provider], r)
	}
	if len(byProvider) == 0 {
		return overall
	}
	overall.ByProvider = make(map[string]Metrics, len(byProvider))
	for provider, recs := range byProvider {
		m := Compute(recs)
		// A provider with almost no traffic yields percentages that swing
		// on single events; reporting them next to a provider with
		// thousands would invite a comparison the data cannot support.
		if m.Prompts < minPromptsPerProvider {
			continue
		}
		overall.ByProvider[provider] = m
	}
	if len(overall.ByProvider) == 0 {
		overall.ByProvider = nil
	}
	return overall
}

// minPromptsPerProvider is the floor for reporting a provider separately.
const minPromptsPerProvider = 20

// Compute rolls transcript records into agent-experience metrics.
//
// Records are grouped into prompt units: an operator instruction and
// everything the agent did before the next one. Entries before the first
// prompt belong to no unit and are ignored rather than attributed to an
// invented one.
func Compute(records []Record) Metrics {
	var m Metrics
	if len(records) == 0 {
		return m
	}
	sorted := sortedByTime(records)

	sessions := map[string]bool{}
	var (
		turnsPerPrompt  []float64
		compactions     int
		totalEdits      int
		reworkEdits     int
		promptsWithTask int
		interrupted     int
		growths         []float64
		durations       []float64
		tokensPer       []float64
		toolsPer        []float64
		firstTry        int
	)

	// Per-unit state, reset at every operator instruction.
	var (
		inUnit       bool
		unitTurns    int
		unitEdited   = map[string]bool{}
		unitTask     bool
		unitStopped  bool
		unitRework   bool
		unitTools    int
		unitTokens   int64
		unitStart    time.Time
		unitLast     time.Time
		lastInputTok int64
	)

	closeUnit := func() {
		if !inUnit {
			return
		}
		turnsPerPrompt = append(turnsPerPrompt, float64(unitTurns))
		toolsPer = append(toolsPer, float64(unitTools))
		if unitTokens > 0 {
			tokensPer = append(tokensPer, float64(unitTokens))
		}
		// Only time a unit that actually produced work. A prompt with no
		// turns after it is the operator typing at the end of a session,
		// and timing it would measure how long they left the window open.
		if unitTurns > 0 && !unitLast.IsZero() && unitLast.After(unitStart) {
			durations = append(durations, unitLast.Sub(unitStart).Seconds())
		}
		if unitTask {
			promptsWithTask++
		}
		if unitStopped {
			interrupted++
		}
		if unitTurns > 0 && !unitRework && !unitStopped && !unitTask {
			firstTry++
		}
	}

	for _, r := range sorted {
		if r.SessionID != "" {
			sessions[r.SessionID] = true
		}
		switch r.Kind {
		case KindPrompt:
			closeUnit()
			inUnit = true
			unitTurns = 0
			unitEdited = map[string]bool{}
			unitTask = false
			unitStopped = false
			unitRework = false
			unitTools = 0
			unitTokens = 0
			unitStart = r.At
			unitLast = time.Time{}
			lastInputTok = 0
		case KindAssistantTurn:
			if !inUnit {
				continue
			}
			unitTurns++
			unitLast = r.At
			unitTokens += r.InputTokens
			if r.InputTokens > 0 {
				if lastInputTok > 0 && r.InputTokens > lastInputTok {
					growths = append(growths, float64(r.InputTokens-lastInputTok))
				}
				lastInputTok = r.InputTokens
			}
		case KindToolUse:
			if !inUnit {
				continue
			}
			unitTools++
			if isEscalation(r.ToolName) {
				unitTask = true
			}
			if isEdit(r.ToolName) && r.FilePath != "" {
				totalEdits++
				if unitEdited[r.FilePath] {
					reworkEdits++
					unitRework = true
				}
				unitEdited[r.FilePath] = true
			}
		case KindInterrupt:
			if inUnit {
				unitStopped = true
			}
		case KindCompaction:
			compactions++
		}
	}
	closeUnit()

	m.Sessions = len(sessions)
	m.Prompts = len(turnsPerPrompt)
	m.MedianTurnsPerPrompt = round1(percentile(turnsPerPrompt, 0.5))
	m.P90TurnsPerPrompt = round1(percentile(turnsPerPrompt, 0.9))
	m.MedianContextGrowthTokens = int64(percentile(growths, 0.5))
	m.MedianSecondsPerPrompt = round1(percentile(durations, 0.5))
	m.P90SecondsPerPrompt = round1(percentile(durations, 0.9))
	m.MedianTokensPerPrompt = int64(percentile(tokensPer, 0.5))
	m.MedianToolCallsPerPrompt = round1(percentile(toolsPer, 0.5))
	m.TotalEdits = totalEdits
	if totalEdits > 0 {
		m.ReworkRatePct = round1(float64(reworkEdits) / float64(totalEdits) * 100)
	}
	if m.Prompts > 0 {
		m.EscalationRatePct = round1(float64(promptsWithTask) / float64(m.Prompts) * 100)
		m.InterruptRatePct = round1(float64(interrupted) / float64(m.Prompts) * 100)
		m.FirstTryRatePct = round1(float64(firstTry) / float64(m.Prompts) * 100)
	}
	if m.Sessions > 0 {
		m.CompactionsPerSession = round1(float64(compactions) / float64(m.Sessions))
	}
	return m
}

// percentile returns the p-th percentile of vs (0 when empty) by linear
// interpolation between ranks.
//
// Interpolating rather than taking the nearest observed value matters at
// small sample sizes: the median of two units costing 3 turns and 1 turn
// is 2, not whichever of them the rounding happens to land on.
func percentile(vs []float64, p float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	s := make([]float64, len(vs))
	copy(s, vs)
	sort.Float64s(s)
	if len(s) == 1 {
		return s[0]
	}
	rank := p * float64(len(s)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if hi >= len(s) {
		hi = len(s) - 1
	}
	if lo == hi {
		return s[lo]
	}
	return s[lo] + (rank-float64(lo))*(s[hi]-s[lo])
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

package coaching

import (
	"context"
	"fmt"
	"strings"

	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/llm"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// llmEnricher upgrades a CoachingEvent's Summary with a 1-2 sentence
// natural-language explanation produced by the operator's configured
// LLM backend. Implements SummaryEnricher.
//
// Designed to be cheap: short system prompt, no streaming, hard limit
// on user prompt length. Heuristic Summary stays available as a
// fallback when the backend errors or returns empty content.
type llmEnricher struct {
	backend llm.Backend
}

// NewLLMEnricher wraps an llm.Backend as a SummaryEnricher. Pass nil
// to keep coaching heuristic-only — Pipeline already special-cases a
// nil Enricher.
func NewLLMEnricher(backend llm.Backend) SummaryEnricher {
	if backend == nil {
		return nil
	}
	return &llmEnricher{backend: backend}
}

const enricherSystemPrompt = `You rewrite developer-tool coaching tips.
Input: a short structured recommendation about wasted LLM tokens.
Output: ONE plain-English sentence (<= 25 words) the developer can act on.
No greetings, no preamble, no markdown. Imperative voice.`

func (e *llmEnricher) Enrich(ctx context.Context, ev *eventschema.CoachingEvent) error {
	if ev == nil {
		return nil
	}
	user := buildEnricherPrompt(ev)
	out, err := e.backend.Generate(ctx, enricherSystemPrompt, user)
	if err != nil {
		return fmt.Errorf("enrich: %w", err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	// Promote LLM text to the headline; keep the heuristic Summary
	// as Details so operators can still see the raw rule output.
	if ev.Details == "" {
		ev.Details = ev.Summary
	}
	ev.Summary = out
	return nil
}

func buildEnricherPrompt(ev *eventschema.CoachingEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Recommendation kind: %s\n", ev.Kind)
	if ev.Summary != "" {
		fmt.Fprintf(&b, "Heuristic summary: %s\n", ev.Summary)
	}
	if ev.Details != "" {
		fmt.Fprintf(&b, "Details: %s\n", truncate(ev.Details, 600))
	}
	if ev.EstimatedSavingsTokens > 0 {
		fmt.Fprintf(&b, "Estimated savings: %d tokens", ev.EstimatedSavingsTokens)
		if ev.EstimatedSavingsUSD > 0 {
			fmt.Fprintf(&b, " ($%.4f)", ev.EstimatedSavingsUSD)
		}
		b.WriteByte('\n')
	}
	if ev.WorkflowID != "" {
		fmt.Fprintf(&b, "Workflow: %s\n", ev.WorkflowID)
	}
	if ev.AgentID != "" {
		fmt.Fprintf(&b, "Agent: %s\n", ev.AgentID)
	}
	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

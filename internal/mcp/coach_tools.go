package mcp

import (
	"context"
	"errors"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/coaching/prompts"
)

// CoachDeps wires the coach prompt tool. Reads JSONL directly so it
// does not need the event store — text never persists.
type CoachDeps struct {
	// JSONLRoot overrides the default ~/.claude/projects scan root.
	// Empty means use the default; the prompts package resolves $HOME.
	JSONLRoot string
}

type coachPromptsInput struct {
	Since     string `json:"since,omitempty" jsonschema:"description=RFC3339 timestamp or duration like '24h' or '7d'; default 7d"`
	Until     string `json:"until,omitempty"`
	SessionID string `json:"session_id,omitempty" jsonschema:"description=Restrict to one Claude Code session id"`
	Limit     int    `json:"limit,omitempty"`
}

// RegisterCoachTools mounts tokenops_coach_prompts on s. The tool
// returns a JSON Findings rollup an agent host can render or feed
// back into prompt-tuning workflows.
func RegisterCoachTools(s *Server, d CoachDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_coach_prompts").
		Description("Score your Claude Code prompting against rule-based heuristics. Walks ~/.claude/projects/**/*.jsonl, extracts human-typed turns, returns length distribution, vague/ack/repeat counts, and concrete recommendations. Prompt text is read at scan time and is NOT persisted to the event store.").
		Handler(func(ctx context.Context, in coachPromptsInput) (string, error) {
			opts := prompts.ExtractOptions{
				Root:      d.JSONLRoot,
				SessionID: in.SessionID,
				Limit:     in.Limit,
			}
			if in.Since != "" {
				since, err := parseCoachWindow(in.Since)
				if err != nil {
					return "", err
				}
				opts.Since = since
			} else {
				opts.Since = time.Now().Add(-7 * 24 * time.Hour)
			}
			if in.Until != "" {
				until, err := parseCoachWindow(in.Until)
				if err != nil {
					return "", err
				}
				opts.Until = until
			}
			extracted, err := prompts.Extract(opts)
			if err != nil {
				return "", err
			}
			findings := prompts.Analyze(extracted)
			return jsonString(findings), nil
		})
	return nil
}

// parseCoachWindow accepts the same syntax as `tokenops coach
// prompts --since`: an RFC3339 timestamp or a duration like "24h",
// "7d". The duration form is interpreted as time-ago-from-now.
func parseCoachWindow(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d), nil
	}
	// Allow "7d" / "30d" — time.ParseDuration doesn't accept "d".
	if len(s) > 1 && s[len(s)-1] == 'd' {
		if d, err := time.ParseDuration(s[:len(s)-1] + "h"); err == nil {
			return time.Now().Add(-d * 24), nil
		}
	}
	return time.Time{}, errors.New("invalid time: expected RFC3339 or duration like '24h' or '7d'")
}

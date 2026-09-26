// Package codexjsonl reads Codex CLI's per-session conversation logs
// (~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-<id>.jsonl) and emits
// one PromptEvent per turn. Each session file contains a stream of
// records with mixed types (session_meta, response_item, agent_message,
// reasoning, event_msg, token_count, …); we key on the token_count
// payload nested inside event_msg, which is the only record carrying
// the per-turn usage block and — critically — the live OpenAI
// `rate_limits` object that surfaces the account's available cap windows
// directly. Window roles and durations vary by plan.
//
// Why this is the best signal source we have for ChatGPT Plus/Pro: it
// is documented (OpenAI Codex CLI reference), updated on every turn,
// and the rate_limits block is what the codex.com UI reads. No
// scraping, no API key, no cookie.
//
// One observed record shape:
//
//	{"timestamp":"2026-04-17T08:45:14.725Z","type":"event_msg","payload":{
//	  "type":"token_count",
//	  "info":{
//	    "total_token_usage":{"input_tokens":31220,"cached_input_tokens":18816,
//	      "output_tokens":289,"reasoning_output_tokens":26,"total_tokens":31509},
//	    "last_token_usage":{...same fields, this turn's delta},
//	    "model_context_window":258400},
//	  "rate_limits":{
//	    "primary":{"used_percent":10.0,"window_minutes":300,"resets_at":1776431887},
//	    "secondary":{"used_percent":2.0,"window_minutes":10080,"resets_at":1777018687},
//	    "plan_type":"plus"}}}
package codexjsonl

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/jsonltail"
)

// Turn is one parsed token_count event. The `last_token_usage` block
// is used (not total) so each emitted envelope represents the delta
// for a single turn — additive aggregation in the store stays correct.
type Turn struct {
	Timestamp      time.Time
	SessionID      string
	Model          string
	InputTokens    int64
	CachedTokens   int64
	OutputTokens   int64
	ReasoningTok   int64
	TotalTokens    int64
	ContextWindow  int64
	RateLimits     RateLimits
	RecordSequence int // monotonic ordinal within the session file, used as dedup key
}

// RateLimits mirrors the Codex CLI's per-turn rate_limits block.
// Anchored under each emitted envelope as Attributes so downstream
// tools (signal_quality classifier, dashboards) can read the live
// OpenAI cap state without re-parsing the JSONL.
type RateLimits struct {
	PrimaryUsedPercent     float64 // primary vendor window %
	PrimaryWindowMinutes   int     // vendor-reported duration
	PrimaryResetsAtUnix    int64
	SecondaryUsedPercent   float64 // secondary vendor window %
	SecondaryWindowMinutes int     // vendor-reported duration
	SecondaryResetsAtUnix  int64
	PlanType               string // plus|pro|team|business
}

// rawLine matches the outer record of a Codex JSONL entry. We only
// inspect event_msg → payload.type=token_count; everything else is
// dropped.
type rawLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type string `json:"type"`
		// Model is stated by turn_context, never by token_count.
		Model string `json:"model"`
		Info  *struct {
			LastTokenUsage *struct {
				InputTokens           int64 `json:"input_tokens"`
				CachedInputTokens     int64 `json:"cached_input_tokens"`
				OutputTokens          int64 `json:"output_tokens"`
				ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
				TotalTokens           int64 `json:"total_tokens"`
			} `json:"last_token_usage"`
			ModelContextWindow int64 `json:"model_context_window"`
		} `json:"info"`
		RateLimits *struct {
			Primary *struct {
				UsedPercent   float64 `json:"used_percent"`
				WindowMinutes int     `json:"window_minutes"`
				ResetsAt      int64   `json:"resets_at"`
			} `json:"primary"`
			Secondary *struct {
				UsedPercent   float64 `json:"used_percent"`
				WindowMinutes int     `json:"window_minutes"`
				ResetsAt      int64   `json:"resets_at"`
			} `json:"secondary"`
			PlanType string `json:"plan_type"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

// sessionMeta captures the session_meta record (first line of every
// rollout file). Used to extract session_id + model_provider when the
// token_count records lack a session reference.
type sessionMeta struct {
	Type    string `json:"type"`
	Payload struct {
		ID            string `json:"id"`
		ModelProvider string `json:"model_provider"`
	} `json:"payload"`
}

// DefaultRoot returns the conventional Codex sessions directory
// (~/.codex/sessions). Operators may override via config.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// FindSessionFiles globs every rollout-*.jsonl under root recursively.
// Codex organises sessions under yyyy/mm/dd/ — we walk that tree.
// Returns paths sorted lexicographically.
func FindSessionFiles(root string) ([]string, error) {
	var matches []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // tolerate transient permission/io errors mid-walk
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".jsonl") && strings.HasPrefix(info.Name(), "rollout-") {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk codex sessions: %w", err)
	}
	return matches, nil
}

// ReadFile parses one Codex JSONL file and yields a Turn per
// token_count event (only when last_token_usage is populated — the
// initial "rate limits only" emit on session start is skipped). The
// session_meta first-line is parsed to seed the session ID. The
// visit callback may return non-nil to abort.
func ReadFile(path string, visit func(Turn) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return readReader(f, visit)
}

func readReader(r io.Reader, visit func(Turn) error) error {
	_, err := readLines(r, &readState{}, true, visit)
	return err
}

// readState is what one rollout line depends on from the lines before it.
// A poller resuming mid-file carries it forward, so a turn keeps the
// session, model and sequence number a read from the start would give it.
type readState struct {
	sessionID string
	// model is carried forward from the most recent turn_context, which
	// is where Codex states it.
	model string
	seq   int
}

// readLines reads r's complete lines through st; see jsonltail.ReadLines
// for what final and the returned byte count mean.
func readLines(r io.Reader, st *readState, final bool, visit func(Turn) error) (int64, error) {
	return jsonltail.ReadLines(r, final, func(line []byte) error {
		return st.visitLine(line, visit)
	})
}

// visitLine parses one rollout line and hands a token_count turn with
// usage to visit. Anything else only advances st.
func (st *readState) visitLine(line []byte, visit func(Turn) error) error {
	if st.sessionID == "" {
		var meta sessionMeta
		if err := json.Unmarshal(line, &meta); err == nil && meta.Type == "session_meta" {
			st.sessionID = meta.Payload.ID
			return nil
		}
	}
	var raw rawLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}
	// Codex states the model on its own turn_context record, ahead of
	// the turns it applies to, and never on the token_count record
	// that carries the usage. Carrying it forward is the only way a
	// Codex turn arrives priceable: without it every one of them
	// entered the store with an empty model, which the spend engine
	// cannot look up, so the whole client reported $0.
	if raw.Type == "turn_context" {
		if m := strings.TrimSpace(raw.Payload.Model); m != "" {
			st.model = m
		}
		return nil
	}
	if raw.Type != "event_msg" || raw.Payload.Type != "token_count" {
		return nil
	}
	// Skip the initial rate-limits-only token_count emit (info nil).
	if raw.Payload.Info == nil || raw.Payload.Info.LastTokenUsage == nil {
		return nil
	}
	u := raw.Payload.Info.LastTokenUsage
	// Zero-token turns are info-only emits we don't need.
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.ReasoningOutputTokens == 0 {
		return nil
	}
	ts, err := time.Parse(time.RFC3339Nano, raw.Timestamp)
	if err != nil {
		return nil
	}
	rl := RateLimits{}
	if raw.Payload.RateLimits != nil {
		rl.PlanType = raw.Payload.RateLimits.PlanType
		if p := raw.Payload.RateLimits.Primary; p != nil {
			rl.PrimaryUsedPercent = p.UsedPercent
			rl.PrimaryWindowMinutes = p.WindowMinutes
			rl.PrimaryResetsAtUnix = p.ResetsAt
		}
		if s := raw.Payload.RateLimits.Secondary; s != nil {
			rl.SecondaryUsedPercent = s.UsedPercent
			rl.SecondaryWindowMinutes = s.WindowMinutes
			rl.SecondaryResetsAtUnix = s.ResetsAt
		}
	}
	st.seq++
	return visit(Turn{
		Timestamp:      ts.UTC(),
		SessionID:      st.sessionID,
		InputTokens:    u.InputTokens,
		CachedTokens:   u.CachedInputTokens,
		OutputTokens:   u.OutputTokens,
		ReasoningTok:   u.ReasoningOutputTokens,
		TotalTokens:    u.TotalTokens,
		Model:          st.model,
		ContextWindow:  raw.Payload.Info.ModelContextWindow,
		RateLimits:     rl,
		RecordSequence: st.seq,
	})
}

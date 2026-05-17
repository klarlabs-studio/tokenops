// Package tools scans Claude Code JSONL session logs for tool_use
// + tool_result blocks and surfaces two operator-facing rates:
// destructive-action rate (DAR) and tool-call success rate (TCS).
//
// DAR is the share of bash invocations matching a destructive
// allow-list (rm -rf, force-push, drop table, etc.). For interactive
// agent work, DAR > 0 deserves operator attention; a healthy session
// runs at 0.
//
// TCS is the inverse failure rate across all tool calls — what
// percent returned without is_error=true. Low TCS means the agent
// is making calls that fail (typos, wrong paths, permission denied)
// and burning turns on retries.
//
// Reads JSONLs directly; tool input + output never persisted to the
// event store. Same privacy model as the prompt coach.
package tools

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ExtractOptions mirrors prompts.ExtractOptions so callers can keep
// one shape across the coaching package family.
type ExtractOptions struct {
	Root      string
	Since     time.Time
	Until     time.Time
	SessionID string
	Limit     int
}

// ToolEvent is one parsed tool_use or tool_result block. The
// extractor pairs them by ToolUseID downstream.
type ToolEvent struct {
	Timestamp  time.Time
	SessionID  string
	IsResult   bool
	ToolUseID  string
	Name       string // populated for tool_use only
	RawCommand string // bash command for tool_use, empty otherwise
	IsError    bool   // populated for tool_result only
}

// Stats is the aggregate roll-up from a single Analyze call.
type Stats struct {
	TotalToolCalls    int            `json:"total_tool_calls"`
	FailedCalls       int            `json:"failed_calls"`
	DestructiveCalls  int            `json:"destructive_calls"`
	SuccessRate       float64        `json:"success_rate_pct"`     // 100 * (total - failed) / total
	DestructiveRate   float64        `json:"destructive_rate_pct"` // 100 * destructive / total
	ByTool            map[string]int `json:"by_tool"`
	DestructiveSample []string       `json:"destructive_sample,omitempty"`
}

// destructiveRE matches Bash commands the operator should review
// before they run. Intentionally narrow — wide enough to catch real
// foot-guns, narrow enough that a typical session stays at 0.
var destructiveRE = regexp.MustCompile(`(?i)\b(rm\s+-rf?|sudo\s+rm|git\s+push\s+--force|git\s+push\s+-f\b|--no-verify|drop\s+table|truncate\s+table|delete\s+from|chmod\s+777|killall\s+|shutdown\s|reboot)\b`)

// rawLine matches the per-line JSONL shape we read. We only descend
// into message.content[] looking for tool_use + tool_result blocks.
type rawLine struct {
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// rawContent is each element of message.content[]. The discriminator
// is `type`; tool_use carries name + input; tool_result carries
// tool_use_id + is_error + content.
type rawContent struct {
	Type      string          `json:"type"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

const scanBufSize = 4 * 1024 * 1024

// Extract walks the JSONL tree and returns every tool event seen.
// Both tool_use and tool_result blocks; callers pair by ToolUseID
// or aggregate independently.
func Extract(opts ExtractOptions) ([]ToolEvent, error) {
	root := opts.Root
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, ".claude", "projects")
	}
	var out []ToolEvent
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if opts.SessionID != "" {
			stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
			if stem != opts.SessionID {
				return nil
			}
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer func() { _ = f.Close() }()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), scanBufSize)
		for scanner.Scan() {
			var line rawLine
			if jsonErr := json.Unmarshal(scanner.Bytes(), &line); jsonErr != nil {
				continue
			}
			ts, terr := time.Parse(time.RFC3339Nano, line.Timestamp)
			if terr != nil {
				continue
			}
			if !opts.Since.IsZero() && ts.Before(opts.Since) {
				continue
			}
			if !opts.Until.IsZero() && ts.After(opts.Until) {
				continue
			}
			if len(line.Message.Content) == 0 || line.Message.Content[0] != '[' {
				continue
			}
			var parts []rawContent
			if jsonErr := json.Unmarshal(line.Message.Content, &parts); jsonErr != nil {
				continue
			}
			for _, p := range parts {
				switch p.Type {
				case "tool_use":
					ev := ToolEvent{
						Timestamp: ts,
						SessionID: line.SessionID,
						ToolUseID: p.ToolUseID,
						Name:      p.Name,
					}
					if p.Name == "Bash" && len(p.Input) > 0 {
						var inp struct {
							Command string `json:"command"`
						}
						if jsonErr := json.Unmarshal(p.Input, &inp); jsonErr == nil {
							ev.RawCommand = inp.Command
						}
					}
					out = append(out, ev)
				case "tool_result":
					out = append(out, ToolEvent{
						Timestamp: ts,
						SessionID: line.SessionID,
						IsResult:  true,
						ToolUseID: p.ToolUseID,
						IsError:   p.IsError,
					})
				}
				if opts.Limit > 0 && len(out) >= opts.Limit {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return nil, err
	}
	return out, nil
}

// Analyze rolls Extract output into per-window Stats. Pairs
// tool_use → tool_result by ToolUseID for the success-rate
// computation; a tool_use with no matching tool_result counts as
// "not failed" (the agent moved on without error).
func Analyze(events []ToolEvent) Stats {
	s := Stats{ByTool: map[string]int{}}
	resultByID := map[string]bool{} // tool_use_id → is_error
	for _, ev := range events {
		if ev.IsResult {
			resultByID[ev.ToolUseID] = ev.IsError
			continue
		}
		s.TotalToolCalls++
		if ev.Name != "" {
			s.ByTool[ev.Name]++
		}
		if ev.Name == "Bash" && destructiveRE.MatchString(ev.RawCommand) {
			s.DestructiveCalls++
			if len(s.DestructiveSample) < 3 {
				s.DestructiveSample = append(s.DestructiveSample, ev.RawCommand)
			}
		}
	}
	for _, ev := range events {
		if !ev.IsResult {
			continue
		}
		if isErr, ok := resultByID[ev.ToolUseID]; ok && isErr {
			s.FailedCalls++
		}
	}
	if s.TotalToolCalls > 0 {
		s.SuccessRate = 100.0 * float64(s.TotalToolCalls-s.FailedCalls) / float64(s.TotalToolCalls)
		s.DestructiveRate = 100.0 * float64(s.DestructiveCalls) / float64(s.TotalToolCalls)
	}
	return s
}

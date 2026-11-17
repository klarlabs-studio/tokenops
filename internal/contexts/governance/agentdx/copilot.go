package agentdx

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/copilotcli"
)

// CopilotDefaultRoot returns Copilot CLI's session-state directory
// (COPILOT_HOME honoured).
func CopilotDefaultRoot() (string, error) { return copilotcli.DefaultRoot() }

// ExtractCopilot reads GitHub Copilot CLI's session logs into Records.
//
// Copilot CLI writes every session as typed events (github/copilot-sdk's
// session events): a user.message is what the operator sent, an abort is
// an interrupt, a session.compaction_complete a compaction, and each tool
// call has its own start and completion. What it does not write is a
// turn's tokens: the per-call usage event is never persisted, so an
// assistant turn carries no context size here.
func ExtractCopilot(opts ExtractOptions) ([]Record, error) {
	root := opts.Root
	if root == "" {
		r, err := CopilotDefaultRoot()
		if err != nil {
			return nil, err
		}
		root = r
	}
	files, err := copilotcli.FindSessionFiles(root)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, path := range files {
		if _, fresh := writtenSince(path, opts.Since); !fresh {
			continue
		}
		f, err := os.Open(path) //nolint:gosec // a session log under Copilot CLI's session-state directory
		if err != nil {
			continue
		}
		out = append(out, readCopilotLog(f, copilotcli.SessionIDFromPath(path), opts.Since, opts.WithPromptText)...)
		_ = f.Close()
	}
	return out, nil
}

// copilotData is the part of an event's data the records need. The
// content of a message is decoded only for an operator's message, and
// only when the scan asks for the words or must check them.
type copilotData struct {
	SessionID       string `json:"sessionId"`
	SelectedModel   string `json:"selectedModel"`
	NewModel        string `json:"newModel"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
	// assistant.message
	MessageID  string `json:"messageId"`
	RequestID  string `json:"requestId"`
	ChunkIndex *int   `json:"chunkIndex"`
	// user.message
	Source                  string `json:"source"`
	IsAutopilotContinuation bool   `json:"isAutopilotContinuation"`
	// tool.execution_start and _complete
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Arguments  json.RawMessage `json:"arguments"`
}

// copilotMetaTools are calls the model makes to the client rather than to
// the work: report_intent sets the status line. They are not tool use.
var copilotMetaTools = map[string]bool{"report_intent": true}

// readCopilotLog flattens one session log.
func readCopilotLog(r io.Reader, sessionID string, since time.Time, withText bool) []Record {
	var (
		out           []Record
		model, effort string
	)
	_ = copilotcli.EachLine(r, func(line []byte) error {
		var env copilotcli.Envelope
		if json.Unmarshal(line, &env) != nil || env.Ephemeral {
			return nil
		}
		at := env.At()
		if at.IsZero() {
			return nil
		}
		var d struct {
			Data copilotData `json:"data"`
		}
		_ = json.Unmarshal(line, &d)
		data := d.Data
		// The session's model and effort hold from where they are set,
		// whether or not the window has reached them yet.
		switch env.Type {
		case copilotcli.TypeSessionStart, copilotcli.TypeSessionResume:
			if data.SessionID != "" {
				sessionID = data.SessionID
			}
			model, effort = firstNonEmpty(data.SelectedModel, model), firstNonEmpty(normEffort(data.ReasoningEffort), effort)
			return nil
		case "session.model_change":
			model, effort = firstNonEmpty(data.NewModel, model), firstNonEmpty(normEffort(data.ReasoningEffort), effort)
			return nil
		}
		if !since.IsZero() && at.Before(since) {
			return nil
		}
		rec := Record{At: at, SessionID: sessionID}
		if env.AgentID != "" {
			// A sub-agent's work is a session of its own, as a Claude
			// Code subagent's transcript is.
			rec.SessionID += "/" + env.AgentID
			rec.Role = RoleSubagent
		}
		switch env.Type {
		case "user.message":
			if data.IsAutopilotContinuation || copilotInjected(data.Source) {
				return nil // the client's own message, not the operator's
			}
			text := copilotMessageText(line)
			if strings.TrimSpace(text) == "" || isInjected(text) {
				return nil
			}
			rec.Kind, rec.Rejects = KindPrompt, IsRejection(text)
			if withText {
				rec.Text = text
			}
		case "assistant.message":
			if data.ChunkIndex != nil && *data.ChunkIndex > 0 {
				return nil // a later chunk of a message already counted
			}
			rec.Kind = KindAssistantTurn
			rec.Model, rec.Effort = firstNonEmpty(data.Model, model), effort
			rec.RequestID = firstNonEmpty(data.RequestID, data.MessageID)
		case "tool.execution_start":
			if data.ToolName == "" || copilotMetaTools[data.ToolName] {
				return nil
			}
			rec.Kind, rec.ToolName, rec.CallID = KindToolUse, data.ToolName, data.ToolCallID
			rec.CallSignature = callSignature(data.ToolName, data.Arguments)
			var args struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(data.Arguments, &args)
			rec.FilePath = args.Path
			switch {
			case strings.EqualFold(data.ToolName, "bash"):
				if cmd, ok := readOnlyCommand(bashCommand(data.Arguments)); ok {
					rec.Command = cmd
				} else {
					rec.Mutates = true
				}
			case isEdit(data.ToolName):
				rec.Mutates = true
			}
		case "tool.execution_complete":
			if data.ToolCallID == "" {
				return nil
			}
			rec.Kind, rec.CallID = KindToolResult, data.ToolCallID
		case "abort":
			rec.Kind = KindInterrupt
		case "session.compaction_complete":
			rec.Kind = KindCompaction
		default:
			return nil
		}
		out = append(out, rec)
		return nil
	})
	return out
}

// copilotMessageText is a user.message's content: read for the scan that
// asks for it, and for the checks that keep the client's own messages out
// of the operator's count, then dropped with the line.
func copilotMessageText(line []byte) string {
	var m struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	_ = json.Unmarshal(line, &m)
	return m.Data.Content
}

// copilotInjected reports whether a user.message's source names the
// client, not the operator: the schema's examples are "skill-<name>" for
// a skill's hidden message and "agent-<id>" for one agent prompting
// another. Any other source, or none, is taken as typed, so a source
// value this reader does not know cannot empty the operator's count.
func copilotInjected(source string) bool {
	return strings.HasPrefix(source, "skill-") || strings.HasPrefix(source, "agent-")
}

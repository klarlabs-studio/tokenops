// Package copilotcli reads GitHub Copilot CLI's local session records and
// surfaces each session's token usage, per model, as TokenOps
// PromptEvents.
//
// Copilot CLI keeps every session under ~/.copilot/session-state
// (COPILOT_HOME replaces ~/.copilot): an append-only event log at
// <session>/events.jsonl, or <session>.jsonl in early versions
// (docs.github.com, "Copilot CLI configuration directory"). The events are
// the SDK's session events (github/copilot-sdk, session-events.schema.json).
//
// Tokens are on disk only in a session's shutdown record. The per-call
// usage event (assistant.usage) is ephemeral: the schema marks it as never
// persisted to the log. A session.shutdown carries modelMetrics, each
// model's requests and input, output, cache-read, cache-write and
// reasoning tokens for the whole session; a resumed session restores that
// accounting from its checkpoints and writes another shutdown with the
// grown totals. A session still running has written none, so its tokens
// arrive when it ends.
//
// Only the event type, id and timestamp of each line are decoded, and the
// data of the start, resume and shutdown records; what the operator typed
// and what the model answered are never read into a Usage.
package copilotcli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Usage is what one shutdown record adds for one model: the growth of the
// session's totals since the session's previous shutdown, or all of them
// for the first.
type Usage struct {
	// ID is the shutdown event's id and the model, unique per session.
	ID        string
	SessionID string
	// Project is the base name of the repository the session ran in, or
	// of its directory outside one; "unknown" when it recorded neither.
	Project string
	CWD     string
	Branch  string
	Model   string
	// Requests is the API calls made to the model, when recorded.
	Requests int64
	// Input is the input tokens as Copilot CLI counts them; CacheRead and
	// CacheWrite are its cached parts (see InputTotal).
	Input      int64
	CacheRead  int64
	CacheWrite int64
	Output     int64
	// Reasoning is the reasoning tokens, when recorded. Whether Output
	// already includes them is not documented; they are kept apart.
	Reasoning int64
	Timestamp time.Time
}

// InputTotal is the input including what the prompt cache read and wrote.
// Copilot CLI's inputTokens is the total, cache included, for every model
// whose figures show it (cache reads within the input); figures whose
// cache exceeds the input cannot include it, and the parts are added.
func (u Usage) InputTotal() int64 {
	if u.CacheRead+u.CacheWrite > u.Input {
		return u.Input + u.CacheRead + u.CacheWrite
	}
	return u.Input
}

// Home is Copilot CLI's configuration directory: COPILOT_HOME, else
// ~/.copilot.
func Home() (string, error) {
	if h := os.Getenv("COPILOT_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".copilot"), nil
}

// DefaultRoot is where Copilot CLI keeps its sessions.
func DefaultRoot() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "session-state"), nil
}

// eventsFile is a session's event log inside its directory.
const eventsFile = "events.jsonl"

// FindSessionFiles lists every session's event log under root: each
// <session>/events.jsonl, and each <session>.jsonl early versions wrote.
// A missing root holds none.
func FindSessionFiles(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // the CLI's operation locks
		}
		path := filepath.Join(root, name)
		switch {
		case e.IsDir():
			if info, err := os.Stat(filepath.Join(path, eventsFile)); err == nil && info.Mode().IsRegular() {
				out = append(out, filepath.Join(path, eventsFile))
			}
		case e.Type().IsRegular() && strings.HasSuffix(name, ".jsonl"):
			out = append(out, path)
		}
	}
	return out, nil
}

// SessionIDFromPath is the session a log belongs to by where it is: its
// directory for events.jsonl, its name for an early flat log.
func SessionIDFromPath(path string) string {
	if filepath.Base(path) == eventsFile {
		return filepath.Base(filepath.Dir(path))
	}
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}

// Event types this package and the session reader read.
const (
	TypeSessionStart  = "session.start"
	TypeSessionResume = "session.resume"
	TypeShutdown      = "session.shutdown"
	TypeContext       = "session.context_changed"
)

// Envelope is the part of every event line read first: enough to decide
// whether its data is wanted, without decoding the data.
type Envelope struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	// AgentID is set on a sub-agent's events.
	AgentID string `json:"agentId"`
	// Ephemeral events are not meant to be on disk; one that is is
	// skipped like the rest.
	Ephemeral bool `json:"ephemeral"`
}

// At is the event's moment, zero when it cannot be read.
func (e Envelope) At() time.Time {
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return at.UTC()
}

// WorkingContext is where a session runs, as session.start and
// session.resume record it (data.context) and session.context_changed
// records a move (its data).
type WorkingContext struct {
	CWD     string `json:"cwd"`
	GitRoot string `json:"gitRoot"`
	Branch  string `json:"branch"`
}

// Project names the context: its repository's directory, else its own.
func (c WorkingContext) Project() string {
	for _, p := range []string{c.GitRoot, c.CWD} {
		if p = strings.TrimRight(filepath.ToSlash(p), "/"); p != "" {
			return filepath.Base(filepath.FromSlash(p))
		}
	}
	return "unknown"
}

// StartData is the part of a session.start or session.resume read here.
type StartData struct {
	SessionID       string          `json:"sessionId"`
	SelectedModel   string          `json:"selectedModel"`
	ReasoningEffort string          `json:"reasoningEffort"`
	Context         *WorkingContext `json:"context"`
}

type modelMetric struct {
	Requests struct {
		Count int64 `json:"count"`
	} `json:"requests"`
	Usage struct {
		InputTokens      int64 `json:"inputTokens"`
		OutputTokens     int64 `json:"outputTokens"`
		CacheReadTokens  int64 `json:"cacheReadTokens"`
		CacheWriteTokens int64 `json:"cacheWriteTokens"`
		ReasoningTokens  int64 `json:"reasoningTokens"`
	} `json:"usage"`
}

// figures are one model's session totals at a shutdown.
type figures struct{ requests, input, output, cacheRead, cacheWrite, reasoning int64 }

func (m modelMetric) figures() figures {
	u := m.Usage
	return figures{m.Requests.Count, u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens, u.ReasoningTokens}
}

// since is what grew from prev to f. Figures that shrank mean the count
// started again, and are taken whole.
func (f figures) since(prev figures) figures {
	d := figures{f.requests - prev.requests, f.input - prev.input, f.output - prev.output,
		f.cacheRead - prev.cacheRead, f.cacheWrite - prev.cacheWrite, f.reasoning - prev.reasoning}
	if d.requests < 0 || d.input < 0 || d.output < 0 || d.cacheRead < 0 || d.cacheWrite < 0 || d.reasoning < 0 {
		return f
	}
	return d
}

func (f figures) tokens() int64 { return f.input + f.output + f.cacheRead + f.cacheWrite + f.reasoning }

// maxLine is the longest event line read; a longer one (a tool result of
// many megabytes) is skipped without ending the scan.
const maxLine = 64 << 20

// ReadFile yields what each shutdown in one session log adds, per model.
func ReadFile(path string, visit func(Usage) error) error {
	f, err := os.Open(path) //nolint:gosec // a Copilot CLI session log found under its session-state directory
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var (
		session = SessionIDFromPath(path)
		where   WorkingContext
		prev    = map[string]figures{}
	)
	return EachLine(f, func(line []byte) error {
		var env Envelope
		if json.Unmarshal(line, &env) != nil || env.Ephemeral {
			return nil // a half-written last line
		}
		switch env.Type {
		case TypeSessionStart, TypeSessionResume:
			var d struct {
				Data StartData `json:"data"`
			}
			if json.Unmarshal(line, &d) != nil {
				return nil
			}
			if d.Data.SessionID != "" {
				session = d.Data.SessionID
			}
			if d.Data.Context != nil && where.CWD == "" {
				where = *d.Data.Context
			}
		case TypeShutdown:
			var d struct {
				Data struct {
					ModelMetrics map[string]modelMetric `json:"modelMetrics"`
				} `json:"data"`
			}
			if json.Unmarshal(line, &d) != nil {
				return nil
			}
			for model, m := range d.Data.ModelMetrics {
				cur := m.figures()
				grew := cur.since(prev[model])
				prev[model] = cur
				if model == "" || grew.tokens() == 0 {
					continue
				}
				if err := visit(Usage{
					ID: env.ID + "|" + model, SessionID: session, Project: where.Project(),
					CWD: where.CWD, Branch: where.Branch, Model: model, Requests: grew.requests,
					Input: grew.input, CacheRead: grew.cacheRead, CacheWrite: grew.cacheWrite,
					Output: grew.output, Reasoning: grew.reasoning, Timestamp: env.At(),
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// EachLine calls fn with each line of r. A line longer than maxLine (a
// tool result of many megabytes) is skipped, not the end of the read.
func EachLine(r io.Reader, fn func([]byte) error) error { return eachLine(r, maxLine, fn) }

func eachLine(r io.Reader, limit int, fn func([]byte) error) error {
	br := bufio.NewReaderSize(r, 1<<16)
	var (
		buf  []byte
		long bool
	)
	for {
		chunk, err := br.ReadSlice('\n')
		if !long {
			if len(buf)+len(chunk) > limit {
				buf, long = buf[:0], true
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case err != nil && !errors.Is(err, io.EOF):
			return err
		}
		if !long && len(bytes.TrimSpace(buf)) > 0 {
			if ferr := fn(bytes.TrimRight(buf, "\r\n")); ferr != nil {
				return ferr
			}
		}
		buf, long = buf[:0], false
		if err != nil {
			return nil
		}
	}
}

// headLines is how far into a session log its working context is looked
// for: the start record opens the log.
const headLines = 50

// errFound ends a head read once the context is found.
var errFound = errors.New("found")

// SessionHead reads where a session ran from its log's first lines: the
// session id and the working context its start (or resume) record names.
func SessionHead(path string) (string, WorkingContext, bool) {
	f, err := os.Open(path) //nolint:gosec // a Copilot CLI session log found under its session-state directory
	if err != nil {
		return "", WorkingContext{}, false
	}
	defer func() { _ = f.Close() }()
	var (
		id    = SessionIDFromPath(path)
		where WorkingContext
		n     int
	)
	err = EachLine(f, func(line []byte) error {
		if n++; n > headLines {
			return errFound
		}
		var env Envelope
		if json.Unmarshal(line, &env) != nil || (env.Type != TypeSessionStart && env.Type != TypeSessionResume) {
			return nil
		}
		var d struct {
			Data StartData `json:"data"`
		}
		if json.Unmarshal(line, &d) != nil {
			return nil
		}
		if d.Data.SessionID != "" {
			id = d.Data.SessionID
		}
		if d.Data.Context != nil && d.Data.Context.CWD != "" {
			where = *d.Data.Context
			return errFound
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFound) {
		return "", WorkingContext{}, false
	}
	return id, where, where.CWD != ""
}

// Package pisessions reads the session transcripts of Pi (the pi coding
// agent) and its fork OMP and surfaces each assistant turn's token usage
// as a TokenOps PromptEvent, under the provider that served it.
//
// Pi appends one JSON line per entry to <root>/<project>/<start>_<id>.jsonl
// under ~/.pi/agent/sessions (OMP: ~/.omp/agent/sessions): a session
// header, model changes, and messages, the assistant's carrying the
// provider, model and usage. Only those fields are decoded; the content
// of a turn and the user's prompts are never read into memory as text.
// Pi's own recorded cost is not used: turns are priced like every other.
// The layout follows CodexBar's Pi scanner (PiSessionCostScanner.swift).
package pisessions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Turn is one assistant response with its usage.
type Turn struct {
	// ID is the entry's id, unique within its session; an entry without
	// one is named by its file and line.
	ID        string
	SessionID string
	// Project is the session directory Pi files it under.
	Project string
	// Provider is the TokenOps provider of the backend Pi called;
	// Backend is Pi's own name for it ("anthropic", "openai-codex").
	Provider eventschema.Provider
	Backend  string
	Model    string
	// Input is uncached input; CacheRead and CacheWrite are the cache's,
	// CacheWrite1h the one-hour part of CacheWrite.
	Input, CacheRead, CacheWrite, CacheWrite1h, Output int64
	Timestamp                                          time.Time
}

// DefaultRoots are Pi's and OMP's session directories that exist:
// PI_CODING_AGENT_SESSION_DIR, else PI_CODING_AGENT_DIR/sessions, else
// ~/.pi/agent/sessions; and ~/.omp/agent/sessions and
// $XDG_DATA_HOME/omp/sessions.
func DefaultRoots() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	pi := filepath.Join(home, ".pi", "agent", "sessions")
	switch {
	case strings.TrimSpace(os.Getenv("PI_CODING_AGENT_SESSION_DIR")) != "":
		pi = os.Getenv("PI_CODING_AGENT_SESSION_DIR")
	case strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")) != "":
		pi = filepath.Join(os.Getenv("PI_CODING_AGENT_DIR"), "sessions")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	var out []string
	for _, r := range []string{pi, filepath.Join(home, ".omp", "agent", "sessions"), filepath.Join(data, "omp", "sessions")} {
		if info, err := os.Stat(r); err == nil && info.IsDir() {
			out = append(out, r)
		}
	}
	return out, nil
}

// FindSessionFiles lists every transcript under root, skipping hidden
// files and directories.
func FindSessionFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(d.Name()), ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

// entry is the part of a transcript line read here.
type entry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	SessionID string          `json:"sessionId"`
	Timestamp json.RawMessage `json:"timestamp"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	ModelID   string          `json:"modelId"`
	Message   *struct {
		Role      string                     `json:"role"`
		Provider  string                     `json:"provider"`
		Model     string                     `json:"model"`
		ModelID   string                     `json:"modelId"`
		Timestamp json.RawMessage            `json:"timestamp"`
		Usage     map[string]json.RawMessage `json:"usage"`
	} `json:"message"`
}

// maxLine bounds one transcript line.
const maxLine = 16 << 20

// ReadFile yields every assistant turn with usage in one transcript whose
// backend TokenOps knows. A turn whose usage does not read is skipped.
func ReadFile(path string, visit func(Turn) error) error {
	f, err := os.Open(path) //nolint:gosec // a Pi transcript found under its session directory
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var (
		session              string
		fallbackP, fallbackM string
		line                 int
	)
	project := filepath.Base(filepath.Dir(path))
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	for sc.Scan() {
		line++
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue // a line being written
		}
		switch e.Type {
		case "session":
			if session == "" {
				session = firstNonEmpty(e.ID, e.SessionID)
			}
			continue
		case "model_change":
			fallbackP, fallbackM = e.Provider, firstNonEmpty(e.ModelID, e.Model)
			continue
		case "message":
		default:
			continue
		}
		m := e.Message
		if m == nil || m.Role != "assistant" || len(m.Usage) == 0 {
			continue
		}
		backend := firstNonEmpty(m.Provider, e.Provider)
		model := firstNonEmpty(m.Model, e.Model, m.ModelID, e.ModelID)
		if backend == "" {
			backend = fallbackP
		}
		if model == "" && backend == fallbackP {
			model = fallbackM
		}
		provider, ok := providerOf(backend)
		if !ok {
			continue
		}
		t, ok := readUsage(m.Usage)
		if !ok {
			continue
		}
		t.ID = e.ID
		if t.ID == "" {
			t.ID = filepath.Base(path) + "#" + strconv.Itoa(line)
		}
		t.SessionID = session
		if t.SessionID == "" {
			t.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		t.Project, t.Provider, t.Backend, t.Model = project, provider, backend, normalizeModel(model)
		t.Timestamp = timestamp(m.Timestamp)
		if t.Timestamp.IsZero() {
			t.Timestamp = timestamp(e.Timestamp)
		}
		if err := visit(t); err != nil {
			return err
		}
	}
	return sc.Err()
}

// providerOf is the TokenOps provider of a Pi backend: Pi's provider IDs
// are models.dev's, as opencode's are, and openai-codex is ChatGPT's
// Codex sign-in.
func providerOf(backend string) (eventschema.Provider, bool) {
	if backend == "openai-codex" {
		return eventschema.ProviderOpenAI, true
	}
	m, ok := providers.Opencode()[backend]
	return m.Provider, ok && m.Provider != ""
}

// normalizeModel drops a vendor prefix: "openai/gpt-5.4" is "gpt-5.4".
func normalizeModel(m string) string {
	if i := strings.LastIndex(m, "/"); i >= 0 {
		return m[i+1:]
	}
	return m
}

// usageKeys are each counter's spellings, the first present winning.
var usageKeys = map[string][]string{
	"input":  {"input", "inputTokens", "input_tokens", "promptTokens", "prompt_tokens"},
	"read":   {"cacheRead", "cacheReadTokens", "cache_read", "cache_read_tokens", "cacheReadInputTokens", "cache_read_input_tokens"},
	"write":  {"cacheWrite", "cacheWriteTokens", "cache_write", "cache_write_tokens", "cacheCreationTokens", "cache_creation_tokens", "cacheCreationInputTokens", "cache_creation_input_tokens"},
	"output": {"output", "outputTokens", "output_tokens", "completionTokens", "completion_tokens"},
	"1h":     {"cacheWrite1h", "cache_write_1h"},
}

// readUsage reads the counters. A malformed counter (negative, not a
// number) spoils the turn rather than falling through to another
// spelling; a one-hour write larger than all writes is malformed too.
func readUsage(u map[string]json.RawMessage) (Turn, bool) {
	var t Turn
	present := false
	for _, f := range []struct {
		key string
		dst *int64
	}{{"input", &t.Input}, {"read", &t.CacheRead}, {"write", &t.CacheWrite}, {"output", &t.Output}, {"1h", &t.CacheWrite1h}} {
		for _, k := range usageKeys[f.key] {
			raw, ok := u[k]
			if !ok {
				continue
			}
			n, ok := count(raw)
			if !ok {
				return Turn{}, false
			}
			*f.dst, present = n, true
			break
		}
	}
	if t.CacheWrite1h == 0 {
		// OMP's spelling: cttl.ephemeral1h.
		var cttl map[string]json.RawMessage
		if json.Unmarshal(u["cttl"], &cttl) == nil {
			for _, k := range []string{"ephemeral1h", "ephemeral_1h"} {
				if raw, ok := cttl[k]; ok {
					n, ok := count(raw)
					if !ok {
						return Turn{}, false
					}
					t.CacheWrite1h = n
					break
				}
			}
		}
	}
	if !present || t.CacheWrite1h > t.CacheWrite || t.Input+t.CacheRead+t.CacheWrite+t.Output == 0 {
		return Turn{}, false
	}
	return t, true
}

// count reads a non-negative token count: an integer, a float (rounded)
// or a numeric string.
func count(raw json.RawMessage) (int64, bool) {
	s := string(bytes.Trim(bytes.TrimSpace(raw), `"`))
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v >= 1e19 {
		return 0, false
	}
	return int64(v + 0.5), true
}

// timestamp reads epoch milliseconds or seconds, or an ISO time.
func timestamp(raw json.RawMessage) time.Time {
	if len(raw) == 0 {
		return time.Time{}
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && n > 0 {
		if n > 1e12 {
			return time.UnixMilli(int64(n)).UTC()
		}
		return time.Unix(int64(n), 0).UTC()
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// Package geminicli reads Gemini CLI's local chat recordings and surfaces
// each model turn's token usage as a TokenOps PromptEvent.
//
// Gemini CLI records every session under ~/.gemini/tmp/<project>/chats/:
// a session-*.json document with a messages array in older versions, and
// a session-*.jsonl stream in newer ones, where a message is appended again
// each time it updates (google-gemini/gemini-cli, chatRecordingService).
// Only the fields that carry usage are decoded; the content and thoughts
// of a turn, and the user's prompts, are never read into memory as text.
package geminicli

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Turn is one model response with its usage.
type Turn struct {
	ID        string
	SessionID string
	// Project names the project: the directory Gemini CLI keys it by, a
	// slug in newer versions and a hash of the path in older ones.
	Project string
	Model   string
	// InputTokens includes cached input and tool-use prompt tokens, as
	// Gemini counts them; CachedTokens is the cached part.
	InputTokens  int
	CachedTokens int
	// OutputTokens includes thinking tokens.
	OutputTokens int
	Timestamp    time.Time
}

// DefaultRoot is Gemini CLI's data directory: GEMINI_CLI_HOME, else
// ~/.gemini, then tmp.
func DefaultRoot() (string, error) {
	if home := os.Getenv("GEMINI_CLI_HOME"); home != "" {
		return filepath.Join(home, ".gemini", "tmp"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "tmp"), nil
}

// message is the part of a recorded message read here.
type message struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Model     string `json:"model"`
	Tokens    *struct {
		Input    int `json:"input"`
		Output   int `json:"output"`
		Cached   int `json:"cached"`
		Thoughts int `json:"thoughts"`
		Tool     int `json:"tool"`
	} `json:"tokens"`
}

// header is a session's first record, or the document around it.
type header struct {
	SessionID string    `json:"sessionId"`
	Messages  []message `json:"messages"`
}

// settled is how long a session file must be untouched before its last
// message is taken as final: a turn still streaming is appended again
// with more tokens, and the store keeps the first copy of an ID.
const settled = time.Minute

// FindSessionFiles lists every chat recording under root.
func FindSessionFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if (strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".jsonl")) &&
			strings.Contains(filepath.ToSlash(path), "/chats/") {
			out = append(out, path)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

// project is the directory Gemini CLI files the session under: the one
// holding chats/, whether the recording sits in chats/ or, for a
// subagent, in a directory below it.
func project(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	p := ""
	for i := len(parts) - 2; i > 0; i-- {
		if parts[i] == "chats" {
			p = parts[i-1]
			break
		}
	}
	if p == "" {
		return "unknown"
	}
	if len(p) == 64 && isHex(p) {
		return "gemini-" + p[:8] // a hash of the project path
	}
	return p
}

func isHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// ReadFile yields every settled model turn with usage in one recording,
// once per message ID with its final figures.
func ReadFile(path string, now time.Time, visit func(Turn) error) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	idle := now.Sub(info.ModTime()) >= settled
	var (
		session string
		msgs    []message
	)
	if strings.HasSuffix(path, ".jsonl") {
		session, msgs, err = readStream(path)
	} else {
		session, msgs, err = readDocument(path)
	}
	if err != nil {
		return err
	}
	// The last version of each ID wins; a message followed by another is
	// finished, and the last one is when the file has gone quiet.
	last := map[string]int{}
	for i, m := range msgs {
		if m.ID != "" {
			last[m.ID] = i
		}
	}
	proj := project(path)
	for i, m := range msgs {
		if m.Type != "gemini" || m.Tokens == nil || m.ID == "" || last[m.ID] != i {
			continue
		}
		if i == len(msgs)-1 && !idle {
			continue
		}
		t := m.Tokens
		if t.Input+t.Output+t.Thoughts+t.Tool == 0 {
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, m.Timestamp)
		if err := visit(Turn{
			ID: m.ID, SessionID: session, Project: proj, Model: m.Model,
			InputTokens: t.Input + t.Tool, CachedTokens: t.Cached, OutputTokens: t.Output + t.Thoughts,
			Timestamp: ts.UTC(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func readDocument(path string) (string, []message, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a Gemini CLI recording found under its data directory
	if err != nil {
		return "", nil, err
	}
	var h header
	if err := json.Unmarshal(b, &h); err != nil {
		return "", nil, err
	}
	return h.SessionID, h.Messages, nil
}

func readStream(path string) (string, []message, error) {
	f, err := os.Open(path) //nolint:gosec // a Gemini CLI recording found under its data directory
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	var (
		session string
		msgs    []message
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var h header
		if session == "" && json.Unmarshal(line, &h) == nil && h.SessionID != "" {
			session = h.SessionID
			continue
		}
		var m message
		if json.Unmarshal(line, &m) != nil || m.Type == "" {
			continue // a control line ($set, $rewindTo) or a partial write
		}
		msgs = append(msgs, m)
	}
	return session, msgs, sc.Err()
}

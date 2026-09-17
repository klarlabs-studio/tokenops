// Package cursorturns is the bridge between Cursor's stop hook and the
// event store.
//
// Cursor is the one client that keeps no per-turn record on disk. Its
// SQLite store holds the conversation, and its usage endpoint reports
// plan consumption — a percentage, not tokens — which is why the
// capability matrix lists Cursor spend as quota only.
//
// But its stop hook hands over the turn's token counts in the payload:
// input, output, cache read and cache write, per generation. Nothing was
// keeping them. This records each one as it arrives so the daemon can
// ingest it, which upgrades Cursor from "how much of the plan is gone"
// to the same per-turn accounting every other client already has.
//
// A ledger rather than a direct write because the hook runs in the
// agent's critical path: appending a line is bounded work, opening the
// event store is not.
package cursorturns

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Turn is one Cursor generation's usage, as the stop hook reported it.
//
// The token fields are pointers because Cursor documents them as
// optional: absent means "not reported", which is not the same as zero
// and must not be recorded as a turn that cost nothing.
type Turn struct {
	TS             time.Time `json:"ts"`
	ConversationID string    `json:"conversation_id"`
	GenerationID   string    `json:"generation_id"`
	Model          string    `json:"model"`
	ModelID        string    `json:"model_id"`

	InputTokens      *int64 `json:"input_tokens,omitempty"`
	OutputTokens     *int64 `json:"output_tokens,omitempty"`
	CacheReadTokens  *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
}

// ModelName prefers the structured id. `model` is the composer slug —
// "cursor-grok-4.6-high-fast" — which bakes in effort and speed flags no
// rate card carries; `model_id` is "grok-4.6", which one might.
func (t Turn) ModelName() string {
	if m := strings.TrimSpace(t.ModelID); m != "" {
		return m
	}
	return strings.TrimSpace(t.Model)
}

// Reported is true when Cursor actually sent usage for this turn.
func (t Turn) Reported() bool { return t.InputTokens != nil || t.OutputTokens != nil }

const ledgerFile = "turns.jsonl"

// DefaultDir is where the ledger lives.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".tokenops-cursor-turns"
	}
	return filepath.Join(home, ".tokenops", "cursor-turns")
}

func resolveDir(dir string) string {
	if dir != "" {
		return dir
	}
	return DefaultDir()
}

// Append records one turn. A turn Cursor did not measure is not written:
// a line with no tokens would become an event asserting a turn cost
// nothing, which is the distinction this package exists to keep.
//
// Errors are swallowed by design. This runs inside the agent's stop
// hook, and losing a spend record is a smaller harm than failing a hook
// the operator's session depends on.
func Append(dir string, t Turn) {
	if !t.Reported() || t.GenerationID == "" {
		return
	}
	dir = resolveDir(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, ledgerFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // ledger, not a secret
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if t.TS.IsZero() {
		t.TS = time.Now().UTC()
	}
	_ = json.NewEncoder(f).Encode(t)
}

// Read returns every recorded turn, oldest first.
//
// The whole ledger is returned on every call rather than tracking a
// marker, because the envelopes built from it carry deterministic ids
// and the store drops a repeat on conflict. A marker would add state
// that can drift out of sync with the store; re-reading cannot.
func Read(dir string) ([]Turn, error) {
	path := filepath.Join(resolveDir(dir), ledgerFile)
	f, err := os.Open(path) //nolint:gosec // our own ledger
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open cursor turn ledger: %w", err)
	}
	defer func() { _ = f.Close() }()

	var out []Turn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var t Turn
		if json.Unmarshal([]byte(line), &t) != nil {
			// One malformed line must not lose the rest of the ledger.
			continue
		}
		out = append(out, t)
	}
	return out, sc.Err()
}

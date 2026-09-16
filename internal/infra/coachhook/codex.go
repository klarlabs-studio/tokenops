package coachhook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Codex Stop hooks hand this package a transcript in a different dialect
// from Claude Code's, and one that has to be priced differently.
//
// The hook payload itself needs no translation at all: Codex sends
// session_id, transcript_path, cwd, hook_event_name and stop_hook_active
// under exactly those names, so the handler reads a Codex Stop event with
// the struct it already had. What differs is the file those events point
// at.

// codexLine is the subset of a Codex rollout record this package needs.
// Usage arrives on its own `token_count` event rather than attached to an
// assistant message, and the model is stated separately by `turn_context`.
type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type  string `json:"type"`
		Model string `json:"model"`
		Info  *struct {
			LastTokenUsage *codexUsage `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// codexUsage is one turn's token accounting.
//
// CachedInputTokens is a SUBSET of InputTokens here, which is the opposite
// of Claude Code, where cache_read_input_tokens and input_tokens are
// disjoint. Verified against 3597 real records: total_tokens equals
// input_tokens + output_tokens exactly, with the cached figure sitting
// inside the input one. Pricing this the Claude Code way would bill the
// cached tokens twice — once at the full input rate and again at the
// cached rate — and overstate every Codex session.
type codexUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	CacheWriteTokens  int64 `json:"cache_write_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
}

// isCodexLine reports whether a record is Codex's dialect rather than
// Claude Code's. Detected from the record itself rather than from the
// path, so a transcript is read correctly wherever the hook payload
// happens to point.
func isCodexLine(b []byte) bool {
	return bytes.Contains(b, []byte(`"payload"`)) &&
		(bytes.Contains(b, []byte(`"token_count"`)) || bytes.Contains(b, []byte(`"turn_context"`)))
}

// codexTurnCostUSD prices one Codex turn at list rates.
//
// The uncached input is what input_tokens holds minus the cached part,
// because the cached part is already inside it. A record that reports
// more cached than input is not something to reason about, so the
// uncached figure floors at zero rather than going negative and crediting
// the operator for tokens they used.
func codexTurnCostUSD(u *codexUsage, model string) float64 {
	if u == nil || model == "" {
		return 0
	}
	r, err := spend.DefaultTable().Lookup(eventschema.ProviderOpenAI, model)
	if err != nil {
		return 0
	}
	cachedRate := r.CachedInputPerMillion
	if cachedRate == 0 {
		cachedRate = r.InputPerMillion
	}
	uncached := u.InputTokens - u.CachedInputTokens
	if uncached < 0 {
		uncached = 0
	}
	return perMillion(uncached, r.InputPerMillion) +
		perMillion(u.CachedInputTokens, cachedRate) +
		perMillion(u.CacheWriteTokens, r.InputPerMillion) +
		perMillion(u.OutputTokens, r.OutputPerMillion)
}

// codexModelFromHead reads the model out of the start of a rollout.
//
// The tail window this package reads does not always reach a
// `turn_context` record: on four real rollouts one had none in its last
// 256 KiB. Without a model every turn in that window prices at zero, and
// a session silently reported as free is the failure this tool exists to
// find — so the head is read as a fallback.
//
// A session that switched models mid-flight is then priced at the one it
// opened with. That is an approximation and it is stated here rather than
// hidden: it is wrong by the difference between two rates, where the
// alternative is wrong by the whole amount.
func codexModelFromHead(path string) string {
	f, err := os.Open(path) //nolint:gosec // path comes from the trusted hook payload
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(io.LimitReader(f, headBytes))
	sc.Buffer(make([]byte, 0, 64<<10), int(headBytes)+1)
	for sc.Scan() {
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 || b[0] != '{' {
			continue
		}
		var cl codexLine
		if json.Unmarshal(b, &cl) != nil {
			continue
		}
		if m := strings.TrimSpace(cl.Payload.Model); m != "" {
			return m
		}
	}
	return ""
}

// headBytes bounds the fallback read. A rollout states its model within
// the first few records; reading further would be a scan of the whole
// file on the hot path of every Stop.
const headBytes int64 = 64 << 10

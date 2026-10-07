package coachhook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// tailBytes is how much of the transcript tail we read. Claude Code jsonl
// lines are large (a full turn's messages), but the last few are enough to
// find the new usage records since the previous Stop — 256 KiB comfortably
// spans several turns without ever reading the whole file.
const tailBytes int64 = 256 << 10

// turnUsage is one turn, normalised across transcript dialects. Cost is
// computed at parse time because only the parser knows which dialect the
// record came from, and the two are priced differently.
type turnUsage struct {
	Timestamp string
	Model     string
	CostUSD   float64
	// Unpriced reports that the turn named a model the rate card does not
	// know, which is not the same as a turn that cost nothing.
	Unpriced bool
	// ContextTokens is how full the window was on this turn: whatever it
	// sent, plus everything read back from cache, plus what was just
	// written to it. Cumulative tokens would answer a different question.
	ContextTokens int64
	// Compaction is set on a compaction boundary rather than a turn: how
	// Claude Code says it was triggered ("manual", "auto").
	Compaction string
}

// readTurns reads the transcript tail and returns each turn's usage,
// whichever client wrote the file.
//
// The dialect is detected from the records rather than from the path or a
// flag, so `coach-hook` is one handler for both clients: Codex sends the
// same Stop payload under the same field names, and this is the only
// place the two actually differ.
func readTurns(path string, cfg Config, now time.Time) []turnUsage {
	raw := tailLines(path)
	out := make([]turnUsage, 0, len(raw))
	// Codex states the model on its own record, ahead of the turns it
	// applies to; carry the most recent one forward.
	codexModel := ""
	sawCodexUsage := false
	for _, b := range raw {
		if isCodexLine(b) {
			var cl codexLine
			if json.Unmarshal(b, &cl) != nil {
				continue
			}
			if m := strings.TrimSpace(cl.Payload.Model); m != "" {
				codexModel = m
			}
			if cl.Payload.Info == nil || cl.Payload.Info.LastTokenUsage == nil {
				continue
			}
			u := cl.Payload.Info.LastTokenUsage
			sawCodexUsage = true
			out = append(out, turnUsage{
				Timestamp: cl.Timestamp,
				Model:     codexModel,
				Unpriced:  true, // settled in priceCodexTurns
				// Cached is inside input here, so the window is input plus
				// what was written to cache — adding the cached figure
				// again would count it twice.
				ContextTokens: u.InputTokens + u.CacheWriteTokens,
			})
			continue
		}
		var tl transcriptLine
		if json.Unmarshal(b, &tl) != nil {
			continue
		}
		if tl.Subtype == "compact_boundary" {
			trigger := tl.CompactMetadata.Trigger
			if trigger == "" {
				trigger = "unknown"
			}
			out = append(out, turnUsage{Timestamp: tl.Timestamp, Compaction: trigger})
			continue
		}
		if tl.Message.Usage == nil {
			continue
		}
		u := tl.Message.Usage
		tbl := cfg.ratesAt(parseTurnTime(tl.Timestamp, now))
		out = append(out, turnUsage{
			Timestamp:     tl.Timestamp,
			Model:         tl.Message.Model,
			CostUSD:       turnCostUSD(tbl, u, tl.Message.Model),
			Unpriced:      !claudePriced(tbl, tl.Message.Model),
			ContextTokens: u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		})
	}
	// A tail that never reached a turn_context leaves every Codex turn
	// unpriced, and a session silently reported as free is worse than one
	// reported approximately. Fall back to the model the rollout opened
	// with.
	if sawCodexUsage && codexModel == "" {
		codexModel = codexModelFromHead(path)
	}
	if sawCodexUsage {
		priceCodexTurns(out, raw, codexModel, cfg, now)
	}
	return out
}

// accumulate sums the full API-equivalent cost of every turn in the transcript
// tail whose timestamp is strictly greater than the session's dedup marker,
// adds it to st.CumulativeUSD, and advances the marker to the newest timestamp
// counted. It returns the model of the newest counted turn (for the ledger).
// Turns whose model can't be priced still
// advance the marker (counted at zero cost) so they are not re-summed later.
// Turns without a timestamp are skipped entirely — without one they cannot be
// deduplicated against future Stops, and real Claude Code turns always carry a
// timestamp. Equal timestamps are treated as already-counted (marker uses
// strict >), an acceptable simplification: between consecutive Stops there is
// normally ~1 new turn and its timestamp is distinct.
func accumulate(path string, st *sessionState, cfg Config, now time.Time) (model string, contextTokens int64, unpriced string) {
	newMarker := st.LastCountedTS
	for _, t := range readTurns(path, cfg, now) {
		if t.Timestamp == "" || t.Timestamp <= st.LastCountedTS {
			continue
		}
		if t.Compaction != "" {
			st.compaction = t.Compaction
			if t.Timestamp > newMarker {
				newMarker = t.Timestamp
			}
			continue
		}
		st.CumulativeUSD += t.CostUSD
		model = t.Model
		contextTokens = t.ContextTokens
		st.countContext(t.ContextTokens, cfg.CompactAtTokens)
		if t.Unpriced && t.Model != "" {
			unpriced = t.Model
		}
		if t.Timestamp > newMarker {
			newMarker = t.Timestamp
		}
	}
	st.LastCountedTS = newMarker
	return model, contextTokens, unpriced
}

// parseTurnTime reads a transcript timestamp for rate selection. An
// unreadable one prices at the latest card rather than at the zero time,
// which would select the oldest card there is.
func parseTurnTime(ts string, fallback time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t
	}
	return fallback
}

func perMillion(tokens int64, ratePerMillion float64) float64 {
	if tokens <= 0 || ratePerMillion <= 0 {
		return 0
	}
	return float64(tokens) * ratePerMillion / 1_000_000.0
}

// tailLines reads the tail of the transcript and returns its whole JSON
// records in file order, leaving them unparsed because which parser
// applies depends on the dialect. Reading only the tail keeps the hook
// cheap on multi-MB transcripts — real rollouts reach 30 MB. Returns nil
// on any read failure (fail open).
func tailLines(path string) [][]byte {
	if path == "" {
		return nil
	}
	f, err := os.Open(path) //nolint:gosec // path comes from the trusted Claude Code hook payload
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil
	}
	size := info.Size()
	start := int64(0)
	if size > tailBytes {
		start = size - tailBytes
	}
	if _, err := f.Seek(start, 0); err != nil {
		return nil
	}
	buf := make([]byte, size-start)
	if _, err := readFull(f, buf); err != nil {
		return nil
	}
	// If we seeked into the middle of a line, drop the leading partial line so
	// we only parse whole JSON records.
	if start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}

	sc := bufio.NewScanner(bytes.NewReader(buf))
	sc.Buffer(make([]byte, 0, 64<<10), int(tailBytes)+1)
	var out [][]byte
	for sc.Scan() {
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 || b[0] != '{' {
			continue
		}
		out = append(out, append([]byte(nil), b...))
	}
	return out
}

// readFull fills buf from r, tolerating short reads. It mirrors io.ReadFull
// without pulling the import for one call site.
func readFull(r *os.File, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			if n == len(buf) {
				return n, nil
			}
			return n, err
		}
	}
	return n, nil
}

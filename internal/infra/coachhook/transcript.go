package coachhook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
)

// tailBytes is the least of a transcript's end a Stop reads again. A read
// starts where the newest turn the last Stop saw begins (see readStart);
// this much is read whatever that offset says, so an offset that no
// longer fits the file costs a Stop nothing.
const tailBytes int64 = 256 << 10

// turnUsage is one turn, normalised across transcript dialects. Cost is
// computed at parse time because only the parser knows which dialect the
// record came from, and the two are priced differently.
type turnUsage struct {
	// Timestamp is the turn's first line's: Claude Code writes a message
	// as one line per content block, each with its own.
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
	// offset is where the turn's first line starts in the file.
	offset int64
	// own reports that the line names the session it was read for as the
	// one that wrote it.
	own bool
	// codex is a Codex turn's usage, priced once the model is settled.
	codex *codexUsage
}

// readTurns reads the transcript from the first line at or after from and
// returns each turn's usage, whichever client wrote the file.
//
// The dialect is detected from the records rather than from the path or a
// flag, so `coach-hook` is one handler for both clients: Codex sends the
// same Stop payload under the same field names, and this is the only
// place the two actually differ.
//
// Claude Code writes an assistant message as one line per content block
// and repeats the message's usage on each, so a message is a turn at its
// first line and its other lines are not turns.
func readTurns(path string, from int64, session string, cfg Config, now time.Time) []turnUsage {
	var out []turnUsage
	// Codex states the model on its own record, ahead of the turns it
	// applies to; carry the most recent one forward.
	codexModel := ""
	sawCodexUsage := false
	messages := claudecodejsonl.Messages{}
	eachLine(path, from, func(at int64, b []byte) {
		if isCodexLine(b) {
			var cl codexLine
			if json.Unmarshal(b, &cl) != nil {
				return
			}
			if m := strings.TrimSpace(cl.Payload.Model); m != "" {
				codexModel = m
			}
			if cl.Payload.Info == nil || cl.Payload.Info.LastTokenUsage == nil {
				return
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
				offset:        at,
				codex:         u,
			})
			return
		}
		if !mayBeClaudeTurn(b) {
			return
		}
		var tl transcriptLine
		if json.Unmarshal(b, &tl) != nil {
			return
		}
		if tl.Subtype == "compact_boundary" {
			trigger := tl.CompactMetadata.Trigger
			if trigger == "" {
				trigger = "unknown"
			}
			out = append(out, turnUsage{Timestamp: tl.Timestamp, Compaction: trigger, offset: at})
			return
		}
		if tl.Message.Usage == nil || !messages.First(tl.Message.ID) {
			return
		}
		u := tl.Message.Usage
		tbl := cfg.ratesAt(parseTurnTime(tl.Timestamp, now))
		out = append(out, turnUsage{
			Timestamp:     tl.Timestamp,
			Model:         tl.Message.Model,
			CostUSD:       turnCostUSD(tbl, u, tl.Message.Model),
			Unpriced:      !claudePriced(tbl, tl.Message.Model),
			ContextTokens: u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
			offset:        at,
			own:           session != "" && tl.SessionID == session,
		})
	})
	// A read that never reached a turn_context leaves every Codex turn
	// unpriced, and a session silently reported as free is worse than one
	// reported approximately. Fall back to the model the rollout opened
	// with.
	if sawCodexUsage && codexModel == "" {
		codexModel = codexModelFromHead(path)
	}
	if sawCodexUsage {
		priceCodexTurns(out, codexModel, cfg, now)
	}
	return out
}

// mayBeClaudeTurn reports whether a Claude Code record can be a turn or a
// compaction boundary. Most of a transcript's bytes are tool results,
// which are neither, and looking is far cheaper than parsing them.
func mayBeClaudeTurn(b []byte) bool {
	return bytes.Contains(b, []byte(`"usage"`)) || bytes.Contains(b, []byte(`compact_boundary`))
}

// readStart is where a Stop starts reading a transcript: at readFrom, the
// first line of the newest turn the last Stop saw, or a tail's length
// before the end when that is earlier.
//
// Everything written since the last Stop is then read, however much it
// is: an instruction can write many megabytes of tool results before the
// agent stops, and a fixed tail lost the turns ahead of them. The newest
// turn is read again because it may not have been finished; the marker
// keeps it from counting twice.
//
// A session with no offset yet reads its transcript from the start. That
// is every session's first Stop, and once for a session whose state was
// written before the offset was kept: its marker still says what counted.
func readStart(path string, readFrom int64) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return max(0, min(readFrom, info.Size()-tailBytes))
}

// sinceOwnStart drops the turns ahead of the session's own first one.
//
// A resumed or forked session starts its transcript with history copied
// from the session it came from. Those lines name that session as their
// writer and this session's lines name this one, so where a transcript
// has a line of the session's own, what stands before it was spent by
// another. A transcript whose lines name no writer, or only one that is
// not the session (Claude Code keeps the first session's name across
// /clear), is the session's from its first line.
func sinceOwnStart(turns []turnUsage) []turnUsage {
	for i, t := range turns {
		if t.own {
			return turns[i:]
		}
	}
	return turns
}

// accumulate sums the full API-equivalent cost of every turn written since
// the session's last Stop, adds it to st.CumulativeUSD, and advances the
// dedup marker to the newest timestamp counted. It returns the model of
// the newest counted turn (for the ledger).
//
// A turn counts when its timestamp is strictly greater than the marker.
// Turns whose model can't be priced still advance the marker (counted at
// zero cost) so they are not re-summed later. Turns without a timestamp
// are skipped entirely — without one they cannot be deduplicated against
// future Stops, and real Claude Code turns always carry a timestamp.
// Equal timestamps are treated as already-counted (marker uses strict >),
// an acceptable simplification: two turns are not written in the same
// millisecond.
func accumulate(path string, st *sessionState, cfg Config, now time.Time) (model string, contextTokens int64, unpriced string) {
	turns := readTurns(path, readStart(path, st.ReadFrom), st.id, cfg, now)
	if st.LastCountedTS == "" {
		turns = sinceOwnStart(turns)
	}
	newMarker := st.LastCountedTS
	for _, t := range turns {
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
	if n := len(turns); n > 0 {
		st.ReadFrom = turns[n-1].offset
	}
	accumulateSubagents(path, st, cfg, now)
	return model, contextTokens, unpriced
}

// accumulateSubagents adds what the session's subagents spent, read from
// their own transcripts beside the session's
// (<session>/subagents/agent-<id>.jsonl), to the session's cumulative
// spend. Each file keeps its own marker and its own offset. A subagent's
// context is its own window, not the session's, so only its cost is
// counted.
func accumulateSubagents(path string, st *sessionState, cfg Config, now time.Time) {
	files, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "agent-*.jsonl"))
	for _, f := range files {
		name := filepath.Base(f)
		marker := st.SubagentCountedTS[name]
		newMarker := marker
		turns := readTurns(f, readStart(f, st.SubagentReadFrom[name]), "", cfg, now)
		for _, t := range turns {
			if t.Timestamp == "" || t.Timestamp <= marker || t.Compaction != "" {
				continue
			}
			st.CumulativeUSD += t.CostUSD
			if t.Timestamp > newMarker {
				newMarker = t.Timestamp
			}
		}
		if newMarker != marker {
			if st.SubagentCountedTS == nil {
				st.SubagentCountedTS = map[string]string{}
			}
			st.SubagentCountedTS[name] = newMarker
		}
		if n := len(turns); n > 0 {
			if st.SubagentReadFrom == nil {
				st.SubagentReadFrom = map[string]int64{}
			}
			st.SubagentReadFrom[name] = turns[n-1].offset
		}
	}
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

// eachLine calls fn with every JSON record of the file from the first
// line that starts at or after from, and with where that line starts. It
// reads a line at a time, so a transcript of any size is never held
// whole; real ones reach hundreds of megabytes. A file that cannot be
// read yields nothing (fail open).
func eachLine(path string, from int64, fn func(at int64, line []byte)) {
	if path == "" {
		return
	}
	f, err := os.Open(path) //nolint:gosec // path comes from the trusted Claude Code hook payload
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	at := int64(0)
	if from > 0 {
		// Start one byte early: what is left of the line that byte sits
		// in is only its newline when from is where a line starts, and
		// the rest of a line cut in two when it is not.
		at = from - 1
		if _, err := f.Seek(at, 0); err != nil {
			return
		}
	}
	r := bufio.NewReaderSize(f, 64<<10)
	if from > 0 {
		rest, err := r.ReadBytes('\n')
		at += int64(len(rest))
		if err != nil {
			return
		}
	}
	for {
		line, err := r.ReadBytes('\n')
		if b := bytes.TrimSpace(line); len(b) > 0 && b[0] == '{' {
			fn(at, b)
		}
		at += int64(len(line))
		if err != nil {
			return
		}
	}
}

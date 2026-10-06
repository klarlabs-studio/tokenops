package coachhook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// readingLostEvery is how often, across every session, the coach repeats
// that a flat-rate plan's window cannot be read. The reading is lost for
// all of them at once; saying it in each would be the same sentence in
// every open terminal.
const readingLostEvery = 6 * time.Hour

// readingLostKind names the note for the quiet policy.
const readingLostKind = "reading_lost"

func readingLostFile(dir string) string { return filepath.Join(dir, "reading-lost.json") }

// evaluateReadingLost says, at most once per readingLostEvery across
// sessions, why a flat-rate plan's window cannot be read. With nothing
// known it is silent: on a flat plan dollars are no fallback.
func evaluateReadingLost(dir string, dec *Decision, st *sessionState, cfg Config, now time.Time) {
	if !cfg.Enabled || cfg.ReadingLost == "" || cfg.Verbosity == verbosityQuiet {
		return
	}
	var latch struct {
		ToldAt time.Time `json:"told_at"`
	}
	if b, err := os.ReadFile(readingLostFile(dir)); err == nil {
		_ = json.Unmarshal(b, &latch)
	}
	if !latch.ToldAt.IsZero() && now.Sub(latch.ToldAt) < readingLostEvery {
		return
	}
	if reason, _ := cfg.hold(readingLostKind, st, now); reason != "" {
		dec.Suppressed = reason
		return
	}
	dec.Nudge, dec.ReadingLost = true, true
	dec.Message = "tokenops: " + cfg.ReadingLost
	latch.ToldAt = now.UTC()
	if b, err := json.Marshal(latch); err == nil {
		_ = os.WriteFile(readingLostFile(dir), b, 0o600)
	}
	st.LastNudgeAt = now.UTC().Format(time.RFC3339Nano)
	st.Nudges++
}

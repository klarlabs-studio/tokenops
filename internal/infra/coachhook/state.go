package coachhook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sessionState is the tiny per-session counter kept beside the ledger.
// CumulativeUSD is the running API-equivalent spend; MaxFiredFraction latches
// the highest budget fraction already alerted (so each tier fires once);
// LastCountedTS is the ISO timestamp of the most recent turn already summed,
// the dedup marker that keeps repeated Stops from double-counting turns still
// present in the tail window.
type sessionState struct {
	CumulativeUSD    float64 `json:"cumulative_usd"`
	MaxFiredFraction float64 `json:"max_fired_fraction"`
	LastCountedTS    string  `json:"last_counted_ts"`
	// LastNudgeAt and Nudges are what the quiet policy is measured
	// against: when the coach last spoke in this session, and how often.
	// Absent on state written before the policy existed, which reads as
	// "never spoken" — the first nudge after an upgrade is never
	// suppressed by a floor nobody recorded.
	LastNudgeAt string `json:"last_nudge_at,omitempty"`
	Nudges      int    `json:"nudges,omitempty"`
	// PromotionNudged latches the read-guard case, so it is argued once
	// per session and never becomes a recurring request.
	PromotionNudged bool `json:"promotion_nudged,omitempty"`
	// OpenTip is the last tip, while it waits to be acted on.
	OpenTip *openTip `json:"open_tip,omitempty"`
	// AboveTurns counts turns at or above CompactAtTokens since the last
	// compaction; LastContext is the newest turn's context, to see one.
	AboveTurns  int   `json:"above_turns,omitempty"`
	LastContext int64 `json:"last_context,omitempty"`
	// CompactTipped latches compact_now until the next compaction.
	CompactTipped bool `json:"compact_tipped,omitempty"`
	// compaction is how the newest compaction this Stop saw was
	// triggered, "" when none; dropFrom is the context just before the
	// newest drop to half or less. Both only live for one Evaluate.
	compaction string
	dropFrom   int64
}

func loadSession(dir, sessionID string) sessionState {
	var st sessionState
	b, err := os.ReadFile(sessionFile(dir, sessionID))
	if err != nil {
		return sessionState{}
	}
	if json.Unmarshal(b, &st) != nil {
		return sessionState{}
	}
	return st
}

// saveSession writes state atomically (temp + rename) so parallel hook
// processes can't corrupt the file. A lost update under a race only means a
// missed/duplicated nudge, never corruption.
func saveSession(dir, sessionID string, st sessionState) {
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	final := sessionFile(dir, sessionID)
	tmp := final + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil { //nolint:gosec // state file, not a secret
		_ = os.Rename(tmp, final)
	}
}

func sessionFile(dir, sessionID string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(sessionID)
	if safe == "" {
		safe = "default"
	}
	return filepath.Join(dir, "session-"+safe+".json")
}

func resolveDir(dir string) string {
	if dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".tokenops-coachhook"
	}
	return filepath.Join(home, ".tokenops", "coach-hook")
}

// parseTime reads a stored timestamp, yielding the zero time for anything
// missing or malformed. A floor cannot be enforced against a time nobody
// recorded, and failing open is the rule for the whole package.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// itoa renders an int64 without importing strconv. Retained for the package's
// test helpers, which build transcript fixtures from token counts.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

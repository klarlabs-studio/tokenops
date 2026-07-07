// Package coachhook is the coaching half of the usage-hooks family: a Claude
// Code Stop hook that watches how much *cache-read* context a session is
// carrying and, when that load crosses a threshold, nudges the operator to
// reclaim it (/compact or a fresh session). Cache-read is the dominant, most
// reclaimable cost in a long Claude Code session — every turn re-bills the
// entire accumulated context at the cache-read rate, so a session dragging a
// large prefix pays that toll on every single turn until it is compacted.
//
// The hook reads only the *tail* of the local transcript jsonl (never the
// whole multi-MB file, never anything off-machine), extracts the most recent
// turn's token usage, and keeps a tiny per-session counter in
// ~/.tokenops/coach-hook/ so it can apply a cooldown between nudges. It is a
// pure coach: it never blocks, never forces the agent to keep going, and
// fails open on every error — a coach must never disrupt the session.
package coachhook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Default thresholds. A session carrying ~1M cache-read tokens per turn is
// already paying a meaningful per-turn toll; the cooldown keeps the nudge
// from firing on every subsequent Stop once the operator has been told.
const (
	// DefaultCacheReadThreshold is the per-turn cache-read token load at or
	// above which the coach nudges.
	DefaultCacheReadThreshold int64 = 1_000_000
	// DefaultCooldownTurns is how many turns must pass after a nudge before
	// the coach will nudge the same session again.
	DefaultCooldownTurns = 20
)

// tailBytes is how much of the transcript tail we read. Claude Code jsonl
// lines are large (a full turn's messages), but the last few are enough to
// find the most recent usage record — 256 KiB comfortably spans several
// turns without ever reading the whole file.
const tailBytes int64 = 256 << 10

// opusCacheReadUSDPerMillionFallback is the cache-read price used only when
// the spend pricing engine can't resolve the model (e.g. an unrecognised
// snapshot). It mirrors the Opus 4.x cache-read rate in the embedded
// pricing.yaml catalog (cached_input_per_million); the live figure is looked
// up from that catalog first via spend.DefaultTable so a rate edit is a data
// change, not a code change.
const opusCacheReadUSDPerMillionFallback = 0.50

// Config tunes the coach. Enabled=false makes Evaluate a no-op (never nudges)
// while still recording the ledger event, so an operator can observe load
// without being nudged.
type Config struct {
	// CacheReadThreshold is the per-turn cache-read token load at/above which
	// the coach nudges.
	CacheReadThreshold int64
	// CooldownTurns is the minimum number of turns between two nudges for the
	// same session (hysteresis, so we don't nag every turn).
	CooldownTurns int
	// Enabled gates nudging. When false the coach observes (ledger only).
	Enabled bool
}

// DefaultConfig returns the shipping defaults: enabled, 1M-token threshold,
// 20-turn cooldown.
func DefaultConfig() Config {
	return Config{
		CacheReadThreshold: DefaultCacheReadThreshold,
		CooldownTurns:      DefaultCooldownTurns,
		Enabled:            true,
	}
}

// Decision is the coach's verdict for one Stop event.
type Decision struct {
	// Nudge is true when the operator should be shown Message.
	Nudge bool
	// Message names the lever (compact / fresh session) and the numbers.
	Message string
	// CacheReadTokens is the most recent turn's cache-read token count.
	CacheReadTokens int64
	// EstCostUSDPerTurn is the API-equivalent per-turn cost of that cache-read
	// load, for models we can price (Opus family). Zero when unpriced.
	EstCostUSDPerTurn float64
}

// usage is the token-usage block Claude Code records on each turn's message.
type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// transcriptLine is the subset of a transcript jsonl record we care about:
// the assistant message's usage and model.
type transcriptLine struct {
	Message struct {
		Model string `json:"model"`
		Usage *usage `json:"usage"`
	} `json:"message"`
}

// sessionState is the tiny per-session counter kept beside the ledger. Turn
// increments once per Stop; LastNudgeTurn records the turn a nudge last fired
// (0 = never nudged) so Evaluate can enforce the cooldown.
type sessionState struct {
	Turn          int `json:"turn"`
	LastNudgeTurn int `json:"last_nudge_turn"`
}

// ledgerEvent is one appended coaching record.
type ledgerEvent struct {
	TS        time.Time `json:"ts"`
	Session   string    `json:"session"`
	Turn      int       `json:"turn"`
	CacheRead int64     `json:"cache_read"`
	Nudged    bool      `json:"nudged"`
	Model     string    `json:"model"`
}

// Evaluate is the coach's decision + side effects for one Stop event. dir is
// the state/ledger root (defaults to ~/.tokenops/coach-hook when empty). It
// reads the tail of transcriptPath, extracts the most recent turn's cache-read
// load, advances the per-session turn counter, decides whether to nudge (with
// cooldown), appends a ledger event, and returns the Decision. now is injected
// for tests. It never returns an error: on any failure it returns a no-nudge
// Decision so the caller can fail open.
func Evaluate(dir, sessionID, transcriptPath string, cfg Config, now time.Time) Decision {
	dir = resolveDir(dir)
	_ = os.MkdirAll(dir, 0o755)

	// Advance the turn counter first — a Stop event is one completed turn,
	// regardless of whether we can read usage this time.
	st := loadSession(dir, sessionID)
	st.Turn++

	line, ok := latestUsageLine(transcriptPath)
	cacheRead := int64(0)
	model := ""
	if ok && line.Message.Usage != nil {
		cacheRead = line.Message.Usage.CacheReadInputTokens
		model = line.Message.Model
	}

	cost := costUSD(cacheRead, model)

	neverNudged := st.LastNudgeTurn == 0
	turnsSince := st.Turn - st.LastNudgeTurn
	overThreshold := cacheRead >= cfg.CacheReadThreshold
	nudge := cfg.Enabled && overThreshold && (neverNudged || turnsSince >= cfg.CooldownTurns)

	dec := Decision{CacheReadTokens: cacheRead, EstCostUSDPerTurn: cost}
	if nudge {
		dec.Nudge = true
		dec.Message = nudgeMessage(cacheRead, cost)
		st.LastNudgeTurn = st.Turn
	}

	saveSession(dir, sessionID, st)
	appendLedger(dir, ledgerEvent{
		TS: now.UTC(), Session: sessionID, Turn: st.Turn,
		CacheRead: cacheRead, Nudged: nudge, Model: model,
	})
	return dec
}

// nudgeMessage builds the operator-facing nudge. It always names the lever
// (/compact or a fresh session); it appends the $ figure only when we could
// price the model (Opus family), otherwise it shows tokens alone.
func nudgeMessage(cacheRead int64, cost float64) string {
	if cost > 0 {
		return "tokenops: this session is carrying ~" + human(cacheRead) +
			" cache-read tokens/turn (~" + humanUSD(cost) +
			"/turn API-equiv) — /compact or a fresh session would cut most of it."
	}
	return "tokenops: this session is carrying ~" + human(cacheRead) +
		" cache-read tokens/turn — /compact or a fresh session would cut most of it."
}

// costUSD returns the API-equivalent per-turn cost of a cache-read load for a
// model we can price. Only Opus-family models are priced (they dominate long
// Claude Code sessions and their cache-read rate is well defined); everything
// else returns 0 so the message shows tokens without a misleading $ figure.
// The rate comes from the spend pricing engine (embedded catalog), falling
// back to a const only when the model can't be resolved.
func costUSD(cacheRead int64, model string) float64 {
	if cacheRead <= 0 || !isOpusFamily(model) {
		return 0
	}
	perMillion := opusCacheReadUSDPerMillionFallback
	if r, err := spend.DefaultTable().Lookup(eventschema.ProviderAnthropic, model); err == nil && r.CachedInputPerMillion > 0 {
		perMillion = r.CachedInputPerMillion
	}
	return float64(cacheRead) / 1_000_000 * perMillion
}

func isOpusFamily(model string) bool {
	return strings.Contains(strings.ToLower(model), "opus")
}

// latestUsageLine reads the tail of the transcript and returns the most recent
// record carrying a message.usage block. Reading only the tail keeps the hook
// cheap on multi-MB transcripts. Returns ok=false on any read/parse failure
// (fail open) or when no usage record is found in the tail window.
func latestUsageLine(path string) (transcriptLine, bool) {
	if path == "" {
		return transcriptLine{}, false
	}
	f, err := os.Open(path) //nolint:gosec // path comes from the trusted Claude Code hook payload
	if err != nil {
		return transcriptLine{}, false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return transcriptLine{}, false
	}
	size := info.Size()
	start := int64(0)
	if size > tailBytes {
		start = size - tailBytes
	}
	if _, err := f.Seek(start, 0); err != nil {
		return transcriptLine{}, false
	}
	buf := make([]byte, size-start)
	if _, err := readFull(f, buf); err != nil {
		return transcriptLine{}, false
	}
	// If we seeked into the middle of a line, drop the leading partial line so
	// we only parse whole JSON records.
	if start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}

	// Scan lines, keeping the last one that has a usage block. Scanning
	// forward (rather than reverse-parsing) is simple and the tail window is
	// small; we allow a large line buffer for big turn records.
	sc := bufio.NewScanner(bytes.NewReader(buf))
	sc.Buffer(make([]byte, 0, 64<<10), int(tailBytes)+1)
	var (
		last  transcriptLine
		found bool
	)
	for sc.Scan() {
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 || b[0] != '{' {
			continue
		}
		var tl transcriptLine
		if json.Unmarshal(b, &tl) != nil {
			continue
		}
		if tl.Message.Usage != nil {
			last = tl
			found = true
		}
	}
	return last, found
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

// Stats summarises the coaching ledger for a `stats` view.
type Stats struct {
	Events              int     `json:"events"`
	DistinctSessions    int     `json:"distinct_sessions"`
	Nudges              int     `json:"nudges"`
	MaxCacheReadPerTurn int64   `json:"max_cache_read_per_turn"`
	AvgCacheReadPerTurn int64   `json:"avg_cache_read_per_turn"`
	EstReclaimableUSD   float64 `json:"est_reclaimable_usd"` // summed cost of nudged turns
}

// ReadStats reads the ledger and aggregates it.
func ReadStats(dir string) (Stats, error) {
	dir = resolveDir(dir)
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return Stats{}, nil
		}
		return Stats{}, err
	}
	defer func() { _ = f.Close() }()

	var s Stats
	sessions := map[string]struct{}{}
	var sumCacheRead int64
	dec := json.NewDecoder(f)
	for {
		var e ledgerEvent
		if err := dec.Decode(&e); err != nil {
			break
		}
		s.Events++
		sessions[e.Session] = struct{}{}
		sumCacheRead += e.CacheRead
		if e.CacheRead > s.MaxCacheReadPerTurn {
			s.MaxCacheReadPerTurn = e.CacheRead
		}
		if e.Nudged {
			s.Nudges++
			s.EstReclaimableUSD += costUSD(e.CacheRead, e.Model)
		}
	}
	s.DistinctSessions = len(sessions)
	if s.Events > 0 {
		s.AvgCacheReadPerTurn = sumCacheRead / int64(s.Events)
	}
	return s, nil
}

// --- helpers ---------------------------------------------------------------

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

func sessionFile(dir, sessionID string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(sessionID)
	if safe == "" {
		safe = "default"
	}
	return filepath.Join(dir, "session-"+safe+".json")
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

func appendLedger(dir string, e ledgerEvent) {
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // ledger, not a secret
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_ = json.NewEncoder(f).Encode(e)
}

// human renders a token count compactly: millions as one decimal (1_400_000
// → "1.4M"), thousands as "k", else the raw integer.
func human(t int64) string {
	switch {
	case t >= 1_000_000:
		whole := t / 1_000_000
		tenths := (t % 1_000_000) / 100_000
		return itoa(whole) + "." + itoa(tenths) + "M"
	case t >= 1000:
		return itoa(t/1000) + "k"
	default:
		return itoa(t)
	}
}

func humanUSD(f float64) string {
	return fmt.Sprintf("$%.2f", f)
}

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

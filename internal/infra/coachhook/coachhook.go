// Package coachhook is the coaching half of the usage-hooks family: a Claude
// Code Stop hook that tracks a session's *cumulative* API-equivalent cost and,
// as that spend crosses fractions of a per-session budget, fires graduated,
// latched nudges to reclaim context (/compact or a fresh session). Cache-read
// is the dominant, most reclaimable cost in a long Claude Code session — every
// turn re-bills the entire accumulated context at the cache-read rate — but the
// damage is done by *accumulation*, not by any single extreme turn: a session
// running thousands of flat turns at a few hundred thousand cache-read tokens
// each quietly compounds into thousands of dollars while no single turn ever
// looks alarming. Phase 1's flat per-turn threshold missed exactly that shape.
//
// The hook reads only the *tail* of the local transcript jsonl (never the whole
// multi-MB file, never anything off-machine), sums the full API-equivalent cost
// of the new turns since it last looked, keeps a tiny per-session counter in
// ~/.tokenops/coach-hook/, and latches each budget-fraction alert so it fires
// once. It is a pure coach: it never blocks, never forces the agent to keep
// going, and fails open on every error — a coach must never disrupt the session.
package coachhook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// DefaultBudgetUSD is the shipping per-session budget. Real sessions that ran
// 7,000–9,300 turns at ~600k cache-read tokens/turn accrued ~$2,400 in
// API-equivalent spend without any single turn being extreme; a $50 budget
// surfaces that drift long before it compounds that far.
const DefaultBudgetUSD = 50.0

// fracEpsilon absorbs float rounding when comparing budget fractions to tier
// boundaries, so a fraction that lands exactly on a boundary still counts as
// having reached it.
const fracEpsilon = 1e-9

// tailBytes is how much of the transcript tail we read. Claude Code jsonl
// lines are large (a full turn's messages), but the last few are enough to
// find the new usage records since the previous Stop — 256 KiB comfortably
// spans several turns without ever reading the whole file.
const tailBytes int64 = 256 << 10

// DefaultTiers are the budget fractions at which the coach nudges before the
// budget is exhausted: half, three-quarters, and the full budget.
func DefaultTiers() []float64 { return []float64{0.50, 0.75, 1.00} }

// Config tunes the coach. Enabled=false makes Evaluate observe-only (it still
// accumulates spend and records the ledger, but never nudges and never latches
// a tier), so an operator can watch a session's cost without being nudged.
type Config struct {
	// BudgetUSD is the per-session API-equivalent budget the fractions are
	// measured against.
	BudgetUSD float64
	// Tiers are the budget fractions (e.g. 0.50, 0.75, 1.00) at which to
	// nudge. Each fires at most once per session (latched).
	Tiers []float64
	// OverBudgetStep re-alerts every additional step once the budget is
	// exceeded: with 1.00 the coach also fires at 200%, 300%, … of budget.
	// Zero disables over-budget escalation.
	OverBudgetStep float64
	// Enabled gates nudging. When false the coach observes (accumulate +
	// ledger only) and never latches a tier.
	Enabled bool
}

// DefaultConfig returns the shipping defaults: enabled, $50 budget, 50/75/100%
// tiers, and over-budget escalation every additional full budget.
func DefaultConfig() Config {
	return Config{
		BudgetUSD:      DefaultBudgetUSD,
		Tiers:          DefaultTiers(),
		OverBudgetStep: 1.00,
		Enabled:        true,
	}
}

// Decision is the coach's verdict for one Stop event.
type Decision struct {
	// Nudge is true when the operator should be shown Message.
	Nudge bool
	// Message names the lever (compact / fresh session) and the numbers.
	Message string
	// CumulativeUSD is the session's total API-equivalent spend so far.
	CumulativeUSD float64
	// BudgetUSD is the budget the fraction is measured against.
	BudgetUSD float64
	// FiredFraction is the budget fraction whose boundary this Stop fired
	// (e.g. 0.50, 1.00, 2.00). Zero when no nudge fired.
	FiredFraction float64
}

// usage is the token-usage block Claude Code records on each turn's message.
type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// transcriptLine is the subset of a transcript jsonl record we care about: the
// top-level ISO8601 timestamp plus the assistant message's usage and model.
type transcriptLine struct {
	Timestamp string `json:"timestamp"`
	Message   struct {
		Model string `json:"model"`
		Usage *usage `json:"usage"`
	} `json:"message"`
}

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
}

// ledgerEvent is one appended coaching record.
type ledgerEvent struct {
	TS            time.Time `json:"ts"`
	Session       string    `json:"session"`
	CumulativeUSD float64   `json:"cumulative_usd"`
	BudgetUSD     float64   `json:"budget_usd"`
	Fraction      float64   `json:"fraction"`
	TierFired     float64   `json:"tier_fired"` // 0 when no tier fired this Stop
	Model         string    `json:"model"`
}

// Evaluate is the coach's decision + side effects for one Stop event. dir is
// the state/ledger root (defaults to ~/.tokenops/coach-hook when empty). It
// loads session state, reads the tail of transcriptPath, sums the full
// API-equivalent cost of every turn newer than the dedup marker into the
// session's cumulative spend, and — if that spend has crossed a budget-fraction
// boundary not yet alerted — nudges at the single highest such boundary
// (latching it). now is injected for tests. It never returns an error: on any
// failure it returns a no-nudge Decision so the caller can fail open.
func Evaluate(dir, sessionID, transcriptPath string, cfg Config, now time.Time) Decision {
	dir = resolveDir(dir)
	_ = os.MkdirAll(dir, 0o755)

	st := loadSession(dir, sessionID)

	model := accumulate(transcriptPath, &st)

	budget := cfg.BudgetUSD
	if budget <= 0 {
		budget = DefaultBudgetUSD
	}
	frac := st.CumulativeUSD / budget

	dec := Decision{CumulativeUSD: st.CumulativeUSD, BudgetUSD: budget}
	fired := highestBoundary(frac, st.MaxFiredFraction, cfg)
	if cfg.Enabled && fired > 0 {
		dec.Nudge = true
		dec.FiredFraction = fired
		dec.Message = nudgeMessage(fired, st.CumulativeUSD, budget)
		st.MaxFiredFraction = fired
	}

	saveSession(dir, sessionID, st)
	appendLedger(dir, ledgerEvent{
		TS: now.UTC(), Session: sessionID,
		CumulativeUSD: st.CumulativeUSD, BudgetUSD: budget,
		Fraction: frac, TierFired: dec.FiredFraction, Model: model,
	})
	return dec
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
func accumulate(path string, st *sessionState) string {
	lines := usageLines(path)
	newMarker := st.LastCountedTS
	model := ""
	for _, tl := range lines {
		if tl.Timestamp == "" || tl.Timestamp <= st.LastCountedTS {
			continue
		}
		st.CumulativeUSD += turnCostUSD(tl.Message.Usage, tl.Message.Model)
		model = tl.Message.Model
		if tl.Timestamp > newMarker {
			newMarker = tl.Timestamp
		}
	}
	st.LastCountedTS = newMarker
	return model
}

// turnCostUSD prices a single turn's full API-equivalent cost: input, output,
// cache-write (cache_creation) and cache-read tokens each at the model's
// per-million rate from the spend catalog. Cache-read uses the cached-input
// rate (falling back to the input rate when the catalog leaves it zero);
// cache-write has no distinct catalog rate, so it is priced at the input rate.
// An unpriceable model yields 0 — the turn still counts (marker advances) but
// adds nothing, so the $ figure never over-states what we can defend.
func turnCostUSD(u *usage, model string) float64 {
	if u == nil {
		return 0
	}
	r, err := spend.DefaultTable().Lookup(eventschema.ProviderAnthropic, model)
	if err != nil {
		return 0
	}
	cacheReadRate := r.CachedInputPerMillion
	if cacheReadRate == 0 {
		cacheReadRate = r.InputPerMillion
	}
	return perMillion(u.InputTokens, r.InputPerMillion) +
		perMillion(u.OutputTokens, r.OutputPerMillion) +
		perMillion(u.CacheCreationInputTokens, r.InputPerMillion) +
		perMillion(u.CacheReadInputTokens, cacheReadRate)
}

func perMillion(tokens int64, ratePerMillion float64) float64 {
	if tokens <= 0 || ratePerMillion <= 0 {
		return 0
	}
	return float64(tokens) * ratePerMillion / 1_000_000.0
}

// highestBoundary returns the single highest budget-fraction boundary the
// session has now reached (frac) that has not yet been alerted
// (> maxFired). Boundaries are the configured Tiers plus, when
// OverBudgetStep>0, 1+k*step for k=1,2,… up to frac. Returning only the
// highest means a Stop that jumps 40%→120% fires the 100% tier alone, never a
// burst of every crossed tier. Zero means nothing new to fire.
func highestBoundary(frac, maxFired float64, cfg Config) float64 {
	best := 0.0
	consider := func(b float64) {
		if b <= frac+fracEpsilon && b > maxFired+fracEpsilon && b > best {
			best = b
		}
	}
	for _, t := range cfg.Tiers {
		if t > 0 {
			consider(t)
		}
	}
	if cfg.OverBudgetStep > 0 {
		for k := 1; k <= 100_000; k++ {
			b := 1.0 + float64(k)*cfg.OverBudgetStep
			if b > frac+fracEpsilon {
				break
			}
			consider(b)
		}
	}
	return best
}

// nudgeMessage builds the operator-facing, escalating nudge for a fired budget
// fraction. It names the lever (/compact or a fresh session) and formats the
// real $ and % from the session's cumulative spend and budget.
func nudgeMessage(frac, cumulative, budget float64) string {
	pct := int(math.Round(frac * 100))
	budgetStr := formatUSD(budget)
	cumStr := fmt.Sprintf("$%.2f", cumulative)
	boundaryStr := formatUSD(budget * frac)
	switch {
	case frac > 1.0+fracEpsilon:
		return fmt.Sprintf("tokenops: this session is at %d%% of your %s budget (%s+). "+
			"Long sessions compound cache-read cost fast — /compact or split the task.",
			pct, budgetStr, boundaryStr)
	case frac >= 1.0-fracEpsilon:
		return fmt.Sprintf("tokenops: over your %s session budget (%s+). "+
			"/compact or start fresh — you're re-reading a large cached context each turn.",
			budgetStr, boundaryStr)
	case frac >= 0.75-fracEpsilon:
		return fmt.Sprintf("tokenops: %d%% of your %s session budget (%s) — consider /compact "+
			"or a fresh session soon; cache-read grows every turn you carry this context.",
			pct, budgetStr, cumStr)
	default:
		return fmt.Sprintf("tokenops: this session is at %s of a %s budget (%d%%) in "+
			"API-equivalent spend — mostly cache-read. A /compact resets the cached context.",
			cumStr, budgetStr, pct)
	}
}

// formatUSD renders a whole-dollar budget without a trailing ".00" ("$50") and
// keeps cents only when present ("$50.50").
func formatUSD(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("$%d", int64(v))
	}
	return fmt.Sprintf("$%.2f", v)
}

// usageLines reads the tail of the transcript and returns every record carrying
// a message.usage block, in file order. Reading only the tail keeps the hook
// cheap on multi-MB transcripts. Returns nil on any read failure (fail open) or
// when no usage record is found in the tail window.
func usageLines(path string) []transcriptLine {
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
	var out []transcriptLine
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
			out = append(out, tl)
		}
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

// Stats summarises the coaching ledger for a `stats` view.
type Stats struct {
	Events           int            `json:"events"`
	DistinctSessions int            `json:"distinct_sessions"`
	Alerts           int            `json:"alerts"`         // total tier firings
	AlertsByTier     map[string]int `json:"alerts_by_tier"` // "50%" -> count, "200%" -> count …
	MaxCumulativeUSD float64        `json:"max_cumulative_usd"`
	TotalEstSpendUSD float64        `json:"total_est_spend_usd"` // sum of each session's peak cumulative
}

// ReadStats reads the ledger and aggregates it. Cumulative spend is a
// per-event snapshot, so per-session peak (the last/highest cumulative) is the
// session's spend; total est spend sums those peaks across sessions.
func ReadStats(dir string) (Stats, error) {
	dir = resolveDir(dir)
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return Stats{AlertsByTier: map[string]int{}}, nil
		}
		return Stats{}, err
	}
	defer func() { _ = f.Close() }()

	s := Stats{AlertsByTier: map[string]int{}}
	peak := map[string]float64{}
	dec := json.NewDecoder(f)
	for {
		var e ledgerEvent
		if err := dec.Decode(&e); err != nil {
			break
		}
		s.Events++
		if e.CumulativeUSD > peak[e.Session] {
			peak[e.Session] = e.CumulativeUSD
		}
		if e.TierFired > 0 {
			s.Alerts++
			s.AlertsByTier[tierLabel(e.TierFired)]++
		}
	}
	s.DistinctSessions = len(peak)
	for _, p := range peak {
		s.TotalEstSpendUSD += p
		if p > s.MaxCumulativeUSD {
			s.MaxCumulativeUSD = p
		}
	}
	return s, nil
}

// tierLabel renders a fired fraction as a percentage label ("50%", "100%",
// "200%") for the stats breakdown.
func tierLabel(frac float64) string {
	return fmt.Sprintf("%d%%", int(math.Round(frac*100)))
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

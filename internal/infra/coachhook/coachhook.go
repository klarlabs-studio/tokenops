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
	// Quiet bounds how often the coach may speak unprompted in one
	// session. Zero values mean the per-finding latches are the whole
	// policy, which is what the hook did before the key existed.
	Quiet Quiet
	// Promotion is the case for letting the read guard start refusing
	// redundant re-reads, built from the operator's own ledger. Empty
	// when the evidence does not justify it, when the guard is already
	// blocking, or when delivery is not `advise` — the level at which the
	// coach makes a case and waits for a human.
	//
	// The caller builds it rather than this package reading the guard's
	// ledger itself, because who is allowed to speak is a delivery
	// question, and delivery lives in the config rather than in the hook.
	Promotion string
}

// Quiet is the rate limit on the coach's proactive channel.
//
// Each finding already latches, so this exists for the case a latch
// cannot see: two *different* findings landing back to back. The floor
// defers — the suppressed finding stays unlatched and speaks once the
// floor has passed — and the cap drops, because a cap that queues is not
// a cap.
type Quiet struct {
	// MinInterval is the floor between two nudges in one session. Zero
	// means no floor.
	MinInterval time.Duration
	// MaxPerSession caps the nudges one session may carry. Zero means no
	// cap.
	MaxPerSession int
}

// silence reports why a nudge must not be spoken now, and whether the
// finding behind it should keep its latch so it can retry later.
//
// Returning the reason rather than a bare bool is what makes the policy
// auditable: it goes into the ledger, so `coach-hook stats` can show that
// the coach had something to say and held it, rather than the operator
// having to infer a working rate limit from an absence.
func (q Quiet) silence(nudges int, last, now time.Time) (reason string, retry bool) {
	if q.MaxPerSession > 0 && nudges >= q.MaxPerSession {
		return "max_per_session", false
	}
	if q.MinInterval > 0 && !last.IsZero() && now.Sub(last) < q.MinInterval {
		return "min_interval", true
	}
	return "", false
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
	// ContextTokens is how full the model's context window was on the
	// newest turn. Zero when no turn was observed.
	ContextTokens int64
	// ContextWindow is the model's window size, when one is known for it.
	ContextWindow int64
	// Suppressed names the quiet rule that held a nudge back
	// ("min_interval", "max_per_session"), empty when nothing was held.
	// The coach had something to say; the operator asked it not to say it
	// yet.
	Suppressed string
	// Promotion is true when the nudge is the read-guard case rather than
	// a budget tier.
	Promotion bool
	// UnpricedModel names a model this session ran on that the rate card
	// does not know, empty when everything was priceable.
	//
	// Without it the session reports $0 and reads as a cheap one. That is
	// the failure this tool exists to find, wearing the tool's own
	// clothes: `gpt-5.5` and `gpt-5.6-luna` are what Codex runs today and
	// neither is in the shipped catalog, so every Codex session would
	// have looked free.
	UnpricedModel string
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
	// Suppressed names the quiet rule that held a nudge back this Stop,
	// empty when nothing was held.
	Suppressed string `json:"suppressed,omitempty"`
	// Promotion records that this Stop argued the read-guard case.
	Promotion bool `json:"promotion,omitempty"`
	// Unpriced names a model the rate card did not know. Without it a
	// session on an uncatalogued model is indistinguishable in the ledger
	// from one that genuinely cost nothing.
	Unpriced string `json:"unpriced,omitempty"`
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

	model, contextTokens, unpriced := accumulate(transcriptPath, &st)

	// A budget the operator set is theirs; the shipping default is not,
	// and the wording depends on which this is.
	budget := cfg.BudgetUSD
	configured := budget > 0 && budget != DefaultBudgetUSD
	if budget <= 0 {
		budget = DefaultBudgetUSD
	}
	frac := st.CumulativeUSD / budget

	dec := Decision{CumulativeUSD: st.CumulativeUSD, BudgetUSD: budget, UnpricedModel: unpriced}
	fired := highestBoundary(frac, st.MaxFiredFraction, cfg)
	if cfg.Enabled && fired > 0 {
		reason, retry := cfg.Quiet.silence(st.Nudges, parseTime(st.LastNudgeAt), now)
		switch {
		case reason == "":
			dec.Nudge = true
			dec.FiredFraction = fired
			dec.Message = nudgeMessage(fired, st.CumulativeUSD, budget, configured)
			// Context is the number that actually constrains the session;
			// lead the dollar figure with it wherever it is known.
			if note := contextNote(contextTokens, model); note != "" {
				dec.Message = note + " " + dec.Message
			}
			dec.ContextTokens = contextTokens
			if w, ok := spend.ContextWindow(model); ok {
				dec.ContextWindow = w
			}
			st.MaxFiredFraction = fired
			st.LastNudgeAt = now.UTC().Format(time.RFC3339Nano)
			st.Nudges++
		case retry:
			// The floor defers rather than drops: leave the tier
			// unlatched so it speaks at the next Stop past the floor.
			// Latching here would silence the finding for the rest of the
			// session, which is a mute, not a rate limit.
			dec.Suppressed = reason
		default:
			// The cap drops. Latch it, so the coach does not re-evaluate
			// a boundary it will never be allowed to speak.
			dec.Suppressed = reason
			st.MaxFiredFraction = fired
		}
	}

	// The budget tier is about the session in flight; the read-guard case
	// is a standing recommendation that will be just as true next turn.
	// So the tier speaks first and the case waits for a later Stop, and
	// at most one thing is said per Stop either way: two findings landing
	// in the same breath is the shape `coaching.quiet` exists to stop,
	// and not saying both at once is the cheapest defence against it.
	if cfg.Enabled && !dec.Nudge && cfg.Promotion != "" && !st.PromotionNudged {
		reason, _ := cfg.Quiet.silence(st.Nudges, parseTime(st.LastNudgeAt), now)
		switch {
		case reason == "":
			dec.Nudge = true
			dec.Promotion = true
			dec.Message = cfg.Promotion
			// Latch only on speaking. A case held back by the rate limit
			// is still a case; latching it here would argue it never.
			st.PromotionNudged = true
			st.LastNudgeAt = now.UTC().Format(time.RFC3339Nano)
			st.Nudges++
		case dec.Suppressed == "":
			dec.Suppressed = reason
		}
	}

	saveSession(dir, sessionID, st)
	appendLedger(dir, ledgerEvent{
		TS: now.UTC(), Session: sessionID,
		CumulativeUSD: st.CumulativeUSD, BudgetUSD: budget,
		Fraction: frac, TierFired: dec.FiredFraction, Model: model,
		Suppressed: dec.Suppressed, Promotion: dec.Promotion,
		Unpriced: dec.UnpricedModel,
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
func accumulate(path string, st *sessionState) (model string, contextTokens int64, unpriced string) {
	newMarker := st.LastCountedTS
	for _, t := range readTurns(path) {
		if t.Timestamp == "" || t.Timestamp <= st.LastCountedTS {
			continue
		}
		st.CumulativeUSD += t.CostUSD
		model = t.Model
		contextTokens = t.ContextTokens
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

// claudePriced and codexPriced report whether the catalog knows a model,
// which is the difference between a turn that was free and a turn nobody
// could put a number on.
func claudePriced(model string) bool {
	_, err := spend.DefaultTable().Lookup(eventschema.ProviderAnthropic, model)
	return model != "" && err == nil
}

func codexPriced(model string) bool {
	_, err := spend.DefaultTable().Lookup(eventschema.ProviderOpenAI, model)
	return model != "" && err == nil
}

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
}

// readTurns reads the transcript tail and returns each turn's usage,
// whichever client wrote the file.
//
// The dialect is detected from the records rather than from the path or a
// flag, so `coach-hook` is one handler for both clients: Codex sends the
// same Stop payload under the same field names, and this is the only
// place the two actually differ.
func readTurns(path string) []turnUsage {
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
		if json.Unmarshal(b, &tl) != nil || tl.Message.Usage == nil {
			continue
		}
		u := tl.Message.Usage
		out = append(out, turnUsage{
			Timestamp:     tl.Timestamp,
			Model:         tl.Message.Model,
			CostUSD:       turnCostUSD(u, tl.Message.Model),
			Unpriced:      !claudePriced(tl.Message.Model),
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
		priceCodexTurns(out, raw, codexModel)
	}
	return out
}

// priceCodexTurns fills in the cost of Codex turns once the model is
// settled, which cannot happen until the whole window has been read.
func priceCodexTurns(out []turnUsage, raw [][]byte, fallbackModel string) {
	i := 0
	for _, b := range raw {
		if !isCodexLine(b) {
			continue
		}
		var cl codexLine
		if json.Unmarshal(b, &cl) != nil {
			continue
		}
		if cl.Payload.Info == nil || cl.Payload.Info.LastTokenUsage == nil {
			continue
		}
		for i < len(out) && out[i].Timestamp != cl.Timestamp {
			i++
		}
		if i >= len(out) {
			return
		}
		model := out[i].Model
		if model == "" {
			model = fallbackModel
			out[i].Model = model
		}
		out[i].CostUSD = codexTurnCostUSD(cl.Payload.Info.LastTokenUsage, model)
		out[i].Unpriced = !codexPriced(model)
		i++
	}
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

// nudgeMessage builds the operator-facing, escalating nudge for a fired
// budget fraction.
//
// Three things about the wording are deliberate, because the earlier
// version got all three wrong and the result read as a bill that did not
// add up.
//
// It does not call the shipping default "your budget". An operator on
// Claude Max pays $200 a month and never chose a $50 figure; a possessive
// on a number they did not set makes it look like a charge they agreed
// to.
//
// Every tier says API-equivalent, not just the quietest one. Previously
// the louder the warning got, the more it read like real money — exactly
// backwards.
//
// And it says outright that nothing is being charged. On a subscription
// this figure is a counterfactual: what the session would have cost at
// list price, which is the only way to see context drift compounding when
// the actual bill is flat and identical either way.
func nudgeMessage(frac, cumulative, budget float64, configured bool) string {
	pct := int(math.Round(frac * 100))
	budgetStr := formatUSD(budget)
	cumStr := fmt.Sprintf("$%.2f", cumulative)

	// Only a budget the operator set is theirs.
	ceiling := "the default " + budgetStr + " session ceiling"
	if configured {
		ceiling = "your " + budgetStr + " session budget"
	}

	switch {
	case frac > 1.0+fracEpsilon:
		return fmt.Sprintf("tokenops: %s API-equivalent this session — %d%% of %s, "+
			"and not a charge. Long sessions compound cache-read fast; /compact or split the task.",
			cumStr, pct, ceiling)
	case frac >= 1.0-fracEpsilon:
		return fmt.Sprintf("tokenops: %s API-equivalent this session, past %s "+
			"(not a charge — your plan bills the same either way). /compact or start fresh; "+
			"you're re-reading a large cached context every turn.",
			cumStr, ceiling)
	case frac >= 0.75-fracEpsilon:
		return fmt.Sprintf("tokenops: %s API-equivalent this session, %d%% of %s "+
			"(not a charge). Consider /compact or a fresh session soon — cache-read grows "+
			"every turn you carry this context.",
			cumStr, pct, ceiling)
	default:
		return fmt.Sprintf("tokenops: %s API-equivalent this session, %d%% of %s — "+
			"mostly cache-read, and not a charge. A /compact resets the cached context.",
			cumStr, pct, ceiling)
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

// Stats summarises the coaching ledger for a `stats` view.
type Stats struct {
	Events           int            `json:"events"`
	DistinctSessions int            `json:"distinct_sessions"`
	Alerts           int            `json:"alerts"`         // total tier firings
	AlertsByTier     map[string]int `json:"alerts_by_tier"` // "50%" -> count, "200%" -> count …
	MaxCumulativeUSD float64        `json:"max_cumulative_usd"`
	TotalEstSpendUSD float64        `json:"total_est_spend_usd"` // sum of each session's peak cumulative
	// Suppressed counts nudges the quiet policy held back, by rule
	// ("min_interval", "max_per_session"). A rate limit you cannot see
	// working is indistinguishable from one that does nothing.
	Suppressed map[string]int `json:"suppressed,omitempty"`
	// PromotionNudges counts the sessions in which the coach argued the
	// read-guard case.
	PromotionNudges int `json:"promotion_nudges,omitempty"`
	// UnpricedModels counts turns per model the rate card could not
	// price. A budget that never moves because nothing could be costed is
	// not a lean session, and this is the only place that difference
	// shows.
	UnpricedModels map[string]int `json:"unpriced_models,omitempty"`
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
		if e.Promotion {
			s.PromotionNudges++
		}
		if e.Unpriced != "" {
			if s.UnpricedModels == nil {
				s.UnpricedModels = map[string]int{}
			}
			s.UnpricedModels[e.Unpriced]++
		}
		if e.Suppressed != "" {
			if s.Suppressed == nil {
				s.Suppressed = map[string]int{}
			}
			s.Suppressed[e.Suppressed]++
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

// contextNote reports how full the model's context window is.
//
// This is the number that matters on a flat-rate plan. The dollar figure
// beside it is a counterfactual — the operator is billed the same either
// way — but context genuinely fills up: it forces a compaction, and until
// then every turn re-reads the whole of it. An operator whose sessions sit
// at 87% of a 1M window is paying for that on every single turn, and no
// amount of dollar framing makes that visible.
//
// An unknown model yields no percentage. A share computed against a
// guessed denominator looks authoritative and is not.
func contextNote(contextTokens int64, model string) string {
	if contextTokens <= 0 {
		return ""
	}
	window, known := spend.ContextWindow(model)
	if !known || window <= 0 {
		return fmt.Sprintf("Context: %s in the window (no published size for %s).",
			formatTokens(contextTokens), model)
	}
	pct := int(math.Round(float64(contextTokens) / float64(window) * 100))
	base := fmt.Sprintf("Context: %s of %s (%d%%)",
		formatTokens(contextTokens), formatTokens(window), pct)

	switch {
	case pct >= 90:
		return base + " — compact now; an automatic compaction is close and it will " +
			"choose what to drop for you."
	case pct >= 75:
		return base + " — worth compacting: every turn now re-reads this whole context."
	case pct >= 50:
		return base + "."
	default:
		return base + "."
	}
}

// formatTokens renders a token count compactly.
func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

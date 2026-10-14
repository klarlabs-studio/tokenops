package avoidable

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// book collects each source's claims on the period's turns.
type book struct {
	in Inputs
	// period are the indexes into in.Turns of the period's turns, oldest
	// first; pos maps an index to its place in period.
	period []int
	pos    map[int]int
	// sessions are each session's turn indexes, oldest first.
	sessions map[string][]int
	claims   []waste.Claim

	noRateCard                       bool
	unpricedRebuilds, unpricedRoutes int
}

func newBook(in Inputs) *book {
	b := &book{in: in, pos: map[int]int{}, sessions: map[string][]int{}, noRateCard: in.Price == nil}
	order := make([]int, len(in.Turns))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, c int) bool { return in.Turns[order[a]].At.Before(in.Turns[order[c]].At) })
	for _, i := range order {
		t := in.Turns[i]
		b.sessions[t.SessionID] = append(b.sessions[t.SessionID], i)
		if !t.At.Before(in.Since) && t.At.Before(in.Until) {
			b.pos[i] = len(b.period)
			b.period = append(b.period, i)
		}
	}
	return b
}

// budget is each period turn's tokens.
func (b *book) budget() []int64 {
	out := make([]int64, len(b.period))
	for p, i := range b.period {
		out[p] = max(b.in.Turns[i].Tokens, 0)
	}
	return out
}

func (b *book) claim(c waste.Class, tokens int64, turns ...int) {
	var at []int
	for _, i := range turns {
		if p, ok := b.pos[i]; ok {
			at = append(at, p)
		}
	}
	if tokens > 0 && len(at) > 0 {
		b.claims = append(b.claims, waste.Claim{Class: c, Turns: at, Tokens: tokens})
	}
}

// claimRereads puts each re-read on the first turn of its session at or
// after it: the turn that carried the file into context.
func (b *book) claimRereads(rs []Reread) {
	for _, r := range rs {
		turns := b.sessions[r.Session]
		k := sort.Search(len(turns), func(k int) bool { return !b.in.Turns[turns[k]].At.Before(r.At) })
		if k < len(turns) {
			b.claim(waste.ClassReread, r.Tokens, turns[k])
		}
	}
}

func usageOf(t analytics.SessionTurn) Usage {
	return Usage{Provider: t.Provider, Model: t.Model, Input: t.InputTokens, CacheRead: t.CacheReadTokens,
		CacheWrite: t.CacheWriteTokens, CacheWrite1h: t.CacheWrite1hTokens, Output: t.OutputTokens}
}

// avoidableShare is the part of a turn's price the changed usage would
// have saved, at the rate card; false when either cannot be priced.
func (b *book) avoidableShare(t analytics.SessionTurn, changed Usage) (float64, bool) {
	if b.in.Price == nil {
		return 0, false
	}
	was, ok := b.in.Price(usageOf(t), t.At)
	if !ok || was <= 0 {
		return 0, false
	}
	now, ok := b.in.Price(changed, t.At)
	if !ok {
		return 0, false
	}
	return max(0, 1-now/was), true
}

func tokensOf(t analytics.SessionTurn, share float64) int64 {
	return int64(math.Round(float64(t.Tokens) * share))
}

// claimCacheRebuilds finds turns that wrote again most of the prefix the
// turn before had cached, and claims the difference between writing that
// part and reading it.
func (b *book) claimCacheRebuilds() {
	for _, turns := range b.sessions {
		for k := 1; k < len(turns); k++ {
			prev, cur := b.in.Turns[turns[k-1]], b.in.Turns[turns[k]]
			if _, in := b.pos[turns[k]]; !in || cur.Model != prev.Model {
				continue
			}
			cached := prev.CacheReadTokens + prev.CacheWriteTokens
			// A turn that fell to under half the context is a compaction
			// or a fresh start: its cache is new by design.
			if cached <= 0 || cur.CacheWriteTokens <= 0 || cur.InputTokens*2 < prev.InputTokens {
				continue
			}
			rebuilt := min(cur.CacheWriteTokens, cached-cur.CacheReadTokens)
			if rebuilt*2 <= cached {
				continue
			}
			if b.noRateCard {
				continue
			}
			changed := usageOf(cur)
			changed.CacheRead += rebuilt
			changed.CacheWrite -= rebuilt
			changed.CacheWrite1h = min(changed.CacheWrite1h, changed.CacheWrite)
			share, ok := b.avoidableShare(cur, changed)
			if !ok {
				b.unpricedRebuilds++
				continue
			}
			b.claim(waste.ClassCacheRebuild, tokensOf(cur, share), turns[k])
		}
	}
}

// sameModel reports whether a turn's model is the one advice named:
// equal, or one the other's dated or aliased form.
func sameModel(turn, advised string) bool {
	t, a := strings.ToLower(turn), strings.ToLower(advised)
	return a != "" && (t == a || strings.Contains(t, a) || strings.Contains(a, t) && t != "")
}

// claimOverTiered claims, for each piece of model advice, the turns of
// its session that stayed on the advised-against model until the next
// advice: the difference to the advised model's price.
func (b *book) claimOverTiered(routes []RouteAdvice) {
	bySession := map[string][]RouteAdvice{}
	for _, r := range routes {
		bySession[r.Session] = append(bySession[r.Session], r)
	}
	for session, rs := range bySession {
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
		for k, r := range rs {
			end := time.Time{}
			if k+1 < len(rs) {
				end = rs[k+1].At
			}
			unpriced := false
			for _, i := range b.sessions[session] {
				t := b.in.Turns[i]
				if t.At.Before(r.At) || !end.IsZero() && !t.At.Before(end) || !sameModel(t.Model, r.From) {
					continue
				}
				if _, in := b.pos[i]; !in || b.noRateCard {
					continue
				}
				changed := usageOf(t)
				changed.Model = r.To
				share, ok := b.avoidableShare(t, changed)
				if !ok {
					unpriced = true
					continue
				}
				b.claim(waste.ClassOverTiered, tokensOf(t, share), i)
			}
			if unpriced {
				b.unpricedRoutes++
			}
		}
	}
}

// claimDetector runs the waste detector over each workflow and claims
// what each finding names.
func (b *book) claimDetector(traces map[string]*workflow.Trace, cfg waste.Config) {
	ids := make([]string, 0, len(traces))
	for id := range traces {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	det := waste.New(cfg)
	for _, id := range ids {
		tr := traces[id]
		if tr == nil || len(tr.Steps) == 0 {
			continue
		}
		b.claimFindings(tr, det.Detect(tr))
	}
}

func (b *book) claimFindings(tr *workflow.Trace, findings []*eventschema.CoachingEvent) {
	// The workflow's turns, and which of them each step was.
	var turns []int
	at := map[int64]int{}
	for _, i := range b.period {
		if t := b.in.Turns[i]; t.WorkflowID == tr.WorkflowID {
			turns = append(turns, i)
			at[t.At.UnixNano()] = i
		}
	}
	if len(turns) == 0 {
		return
	}
	stepTurn := func(s workflow.Step) (int, bool) {
		if s.Envelope == nil {
			return 0, false
		}
		i, ok := at[s.Envelope.Timestamp.UnixNano()]
		return i, ok
	}
	// A finding's estimate is over the whole workflow; the period carries
	// its share of the steps.
	inPeriod := 0
	for _, s := range tr.Steps {
		if _, ok := stepTurn(s); ok {
			inPeriod++
		}
	}
	scaled := func(n int64) int64 { return n * int64(inPeriod) / int64(len(tr.Steps)) }
	late := false
	for _, f := range findings {
		late = late || f.Summary == waste.SummaryLateCompaction
	}
	for _, f := range findings {
		switch f.Summary {
		case waste.SummaryRunawayGrowth:
			b.claim(waste.ClassRetryLoop, scaled(f.EstimatedSavingsTokens), turns...)
		case waste.SummaryIdenticalPrompt:
			for k := 1; k < len(tr.Steps); k++ {
				prev, cur := tr.Steps[k-1].Prompt, tr.Steps[k].Prompt
				if prev == nil || cur == nil || cur.PromptHash == "" || cur.PromptHash != prev.PromptHash {
					continue
				}
				if i, ok := stepTurn(tr.Steps[k]); ok {
					b.claim(waste.ClassRetryLoop, b.in.Turns[i].Tokens, i)
				}
			}
		case waste.SummaryAgentLoop:
			start, _ := strconv.Atoi(f.ReplayMetadata["run_start"])
			length, _ := strconv.Atoi(f.ReplayMetadata["run_length"])
			limit, _ := strconv.Atoi(f.ReplayMetadata["limit"])
			for k := start + limit; k < start+length && k < len(tr.Steps); k++ {
				if i, ok := stepTurn(tr.Steps[k]); ok {
					b.claim(waste.ClassRetryLoop, b.in.Turns[i].Tokens, i)
				}
			}
		case waste.SummaryLateCompaction:
			line, _ := strconv.ParseInt(f.ReplayMetadata["compact_at_tokens"], 10, 64)
			var above []int
			for _, i := range turns {
				if b.in.Turns[i].InputTokens >= line {
					above = append(above, i)
				}
			}
			b.claim(waste.ClassOversized, scaled(f.EstimatedSavingsTokens), above...)
		case waste.SummaryOversized:
			if late {
				// The stretches above the compaction line already hold
				// the peak.
				continue
			}
			peak := turns[0]
			for _, i := range turns {
				if b.in.Turns[i].InputTokens > b.in.Turns[peak].InputTokens {
					peak = i
				}
			}
			b.claim(waste.ClassOversized, f.EstimatedSavingsTokens, peak)
		}
	}
}

// claimAbandoned claims every turn of a session that has been over for a
// day with no commit within a day of any turn and no outcome recorded. A
// session the commit join cannot place (outside a repository, directory
// unknown) is not judged.
func (b *book) claimAbandoned(in Inputs) {
	outcome := uniteconomics.OutcomeJoin(in.Outcomes)
	for _, turns := range b.sessions {
		last := in.Turns[turns[len(turns)-1]]
		if in.Until.Sub(last.At) < commits.MaxGap {
			continue
		}
		abandoned := true
		var total int64
		for _, i := range turns {
			t := in.Turns[i]
			total += t.Tokens
			placed, _ := commits.Place(t, in.Dirs, in.RootOf, in.CommitsOf)
			_, judged := outcome(t)
			if placed == commits.OnCommit || placed == commits.OutsideARepository || placed == commits.UnknownDirectory || judged {
				abandoned = false
				break
			}
		}
		if abandoned {
			b.claim(waste.ClassAbandoned, total, turns...)
		}
	}
}

// rateOf is a session's API-equivalent value per token, or the whole
// period's when the session has no turns.
func (b *book) rateOf(session string) float64 {
	rate := func(idx []int) float64 {
		var usd float64
		var tokens int64
		for _, i := range idx {
			usd += b.in.Turns[i].APIEquivalentUSD
			tokens += b.in.Turns[i].Tokens
		}
		if tokens <= 0 {
			return 0
		}
		return usd / float64(tokens)
	}
	if r := rate(b.sessions[session]); r > 0 {
		return r
	}
	return rate(b.period)
}

func (b *book) notes() []string {
	var out []string
	if b.noRateCard {
		out = append(out, "no rate card: cache rebuilds and over-tiered turns are not priced")
	}
	if b.unpricedRebuilds > 0 {
		out = append(out, fmt.Sprintf("%d cache rebuilds not priced: their model has no list price", b.unpricedRebuilds))
	}
	if b.unpricedRoutes > 0 {
		out = append(out, fmt.Sprintf("%d pieces of model advice not priced: a model in them has no list price", b.unpricedRoutes))
	}
	return out
}

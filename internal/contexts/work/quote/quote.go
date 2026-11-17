// Package quote prices a task before it starts, from the developer's own
// history of similar work: what instructions like it cost at list prices
// (the median and the 90th percentile), the tokens they processed, and
// the share of each current plan window that would take.
//
// A quote is an estimate, never a promise, and it is labelled so
// everywhere. It is drawn only from measured instructions — the turns a
// client recorded answering each one, priced at list — never from an
// agent's guess of what a task will take. Below MinSamples instructions
// of the same model it gives no number at all: a percentile drawn from a
// handful of tasks reads as knowledge it is not.
//
// The package is pure: callers supply the history and the windows.
package quote

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Lookback is how far back the history a quote is drawn from reaches: a
// month, so a change in how the developer works ages out.
const Lookback = 30 * 24 * time.Hour

// MinSamples is the fewest instructions a quote is drawn from. A 90th
// percentile needs a tail to read; below twenty it is one task's luck.
const MinSamples = 20

// Sample is one past instruction: what it ran on and what it cost.
type Sample struct {
	// Client is the agent the instruction was typed into (claude-code,
	// codex, opencode); Provider whose plan or bill its turns went to.
	Client   string `json:"client,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model"`
	// Effort is the reasoning effort the client recorded; empty when it
	// did not.
	Effort string `json:"effort,omitempty"`
	// Kind is the kind of work (optimization/taskclass); empty when the
	// words did not say.
	Kind string    `json:"kind,omitempty"`
	At   time.Time `json:"at"`
	// USD is the instruction's turns at list prices; Tokens what they
	// processed, cache reads included.
	USD    float64 `json:"usd"`
	Tokens int64   `json:"tokens"`
}

// Want is the task about to start.
type Want struct {
	// Client narrows the history to one agent; empty takes every client.
	Client string `json:"client,omitempty"`
	Model  string `json:"model"`
	// Effort and Kind narrow the history when known.
	Effort string `json:"effort,omitempty"`
	Kind   string `json:"kind,omitempty"`
}

// Basis says which history the quote was drawn from.
type Basis string

// Bases, most specific first.
const (
	BasisModelEffortKind Basis = "model_effort_kind"
	BasisModelKind       Basis = "model_kind"
	BasisModelEffort     Basis = "model_effort"
	BasisModel           Basis = "model"
)

// words names a basis in a sentence.
func (b Basis) words() string {
	switch b {
	case BasisModelEffortKind:
		return "the same model, effort and kind of work"
	case BasisModelKind:
		return "the same model and kind of work"
	case BasisModelEffort:
		return "the same model and effort"
	case BasisModel:
		return "the same model"
	}
	return ""
}

// Status says whether a quote was given.
type Status string

// Statuses.
const (
	StatusQuoted       Status = "quoted"
	StatusInsufficient Status = "insufficient_history"
)

// Window is one current plan window and the rate a percent of it costs.
type Window struct {
	Provider string        `json:"provider,omitempty"`
	Name     string        `json:"name"`
	Duration time.Duration `json:"-"`
	UsedPct  float64       `json:"used_pct"`
	ResetsIn time.Duration `json:"-"`
	// USDPerPct is the list value one percent of this kind of window
	// held, measured over CalibratedOn past windows; zero when none was
	// measured.
	USDPerPct    float64 `json:"usd_per_pct,omitempty"`
	CalibratedOn int     `json:"calibrated_on,omitempty"`
}

// WindowShare is the share of one plan window the task would take.
type WindowShare struct {
	Provider string  `json:"provider,omitempty"`
	Window   string  `json:"window"`
	LeftPct  float64 `json:"left_pct"`
	ResetsIn string  `json:"resets_in,omitempty"`
	// Known is false when the window's rate was never measured: then
	// there is no share, only what is left.
	Known  bool    `json:"known"`
	P50Pct float64 `json:"p50_pct,omitempty"`
	P90Pct float64 `json:"p90_pct,omitempty"`
	// LeftAfterP90Pct is what would be left after a p90 task; FitsP90
	// whether a p90 task fits in what is left.
	LeftAfterP90Pct float64 `json:"left_after_p90_pct"`
	FitsP90         bool    `json:"fits_p90"`
	Note            string  `json:"note"`
}

// Quote is the price of a task before it starts.
type Quote struct {
	Status Status `json:"status"`
	// Estimate is always true: a quote is a label as much as a number.
	Estimate     bool   `json:"estimate"`
	Want         Want   `json:"want"`
	Basis        Basis  `json:"basis,omitempty"`
	Samples      int    `json:"samples"`
	MinSamples   int    `json:"min_samples"`
	LookbackDays int    `json:"lookback_days"`
	Provider     string `json:"provider,omitempty"`
	// P50USD and P90USD are list prices: what the task costs on an API
	// key, its value where a plan covers it.
	P50USD    float64       `json:"p50_usd,omitempty"`
	P90USD    float64       `json:"p90_usd,omitempty"`
	P50Tokens int64         `json:"p50_tokens,omitempty"`
	P90Tokens int64         `json:"p90_tokens,omitempty"`
	Windows   []WindowShare `json:"windows,omitempty"`
	Summary   string        `json:"summary"`
	Caveat    string        `json:"caveat"`
}

// Quoted reports whether the quote carries a number.
func (q Quote) Quoted() bool { return q.Status == StatusQuoted }

// Caveat travels with every quote.
const Caveat = "An estimate from your own past instructions on the same model, priced at list: half cost up to the p50 and nine in ten up to the p90. " +
	"A window's share converts that price at the list value one percent of the window held in your past windows; vendors weigh usage in ways they do not publish, so it is approximate."

// Build quotes want from history, read at now, against the current
// windows of the plan the task runs on.
func Build(history []Sample, want Want, windows []Window, now time.Time) Quote {
	q := Quote{Status: StatusInsufficient, Estimate: true, Want: want, MinSamples: MinSamples,
		LookbackDays: int(Lookback / (24 * time.Hour)), Caveat: Caveat}
	basis, picked, most := pick(recent(history, now), want)
	if basis == "" {
		q.Samples = most
		q.Summary = fmt.Sprintf("no quote: %d past instructions on %s in the last %d days, too few for a p50/p90 (needs %d)",
			most, modelWords(want.Model), q.LookbackDays, MinSamples)
		q.Windows = shares(windows, 0, 0)
		return q
	}
	usd := make([]float64, len(picked))
	tokens := make([]float64, len(picked))
	providers := map[string]int{}
	for i, s := range picked {
		usd[i], tokens[i] = s.USD, float64(s.Tokens)
		providers[s.Provider]++
	}
	sort.Float64s(usd)
	sort.Float64s(tokens)
	p50, p90 := quantile(usd, 0.5), quantile(usd, 0.9)
	q.Status, q.Basis, q.Samples, q.Provider = StatusQuoted, basis, len(picked), majority(providers)
	q.P50USD, q.P90USD = round2(p50), round2(p90)
	q.P50Tokens, q.P90Tokens = int64(math.Round(quantile(tokens, 0.5))), int64(math.Round(quantile(tokens, 0.9)))
	q.Windows = shares(windows, p50, p90)
	q.Summary = fmt.Sprintf("estimate: $%.2f typical, $%.2f at the p90 at list prices (%d past instructions on %s, last %d days)",
		q.P50USD, q.P90USD, q.Samples, basis.words(), q.LookbackDays)
	return q
}

// recent keeps the history inside the lookback.
func recent(history []Sample, now time.Time) []Sample {
	since := now.Add(-Lookback)
	out := make([]Sample, 0, len(history))
	for _, s := range history {
		if !s.At.Before(since) && !s.At.After(now) {
			out = append(out, s)
		}
	}
	return out
}

// pick chooses the most specific history with at least MinSamples
// instructions. most is the largest count any level had, for saying how
// far short the history fell.
func pick(history []Sample, want Want) (Basis, []Sample, int) {
	type level struct {
		basis        Basis
		effort, kind bool
		needs        bool
		matched      []Sample
	}
	levels := []*level{
		{basis: BasisModelEffortKind, effort: true, kind: true, needs: want.Effort != "" && want.Kind != ""},
		{basis: BasisModelKind, kind: true, needs: want.Kind != ""},
		{basis: BasisModelEffort, effort: true, needs: want.Effort != ""},
		{basis: BasisModel, needs: true},
	}
	for _, s := range history {
		if !sameModel(s.Model, want.Model) || (want.Client != "" && !strings.EqualFold(s.Client, want.Client)) {
			continue
		}
		for _, l := range levels {
			if !l.needs || (l.effort && !strings.EqualFold(s.Effort, want.Effort)) || (l.kind && s.Kind != want.Kind) {
				continue
			}
			l.matched = append(l.matched, s)
		}
	}
	most := 0
	for _, l := range levels {
		if l.needs && len(l.matched) >= MinSamples {
			return l.basis, l.matched, len(l.matched)
		}
		most = max(most, len(l.matched))
	}
	return "", nil, most
}

// sameModel compares model ids without regard to case, and a dated id
// with its alias: claude-opus-4-5-20251101 is claude-opus-4-5.
func sameModel(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	return a == b || undated(a) == undated(b)
}

// undated drops a trailing -YYYYMMDD.
func undated(m string) string {
	i := strings.LastIndexByte(m, '-')
	if i < 0 || len(m)-i-1 != 8 {
		return m
	}
	for _, c := range m[i+1:] {
		if c < '0' || c > '9' {
			return m
		}
	}
	return m[:i]
}

// shares converts p50 and p90 into each window's share. p90 zero means
// there is no quote: each window says only what is left.
func shares(windows []Window, p50, p90 float64) []WindowShare {
	out := make([]WindowShare, 0, len(windows))
	for _, w := range windows {
		left := math.Max(0, 100-w.UsedPct)
		s := WindowShare{Provider: w.Provider, Window: w.Name, LeftPct: round1(left), LeftAfterP90Pct: round1(left)}
		if w.ResetsIn > 0 {
			s.ResetsIn = durationWords(w.ResetsIn)
		}
		switch {
		case p90 <= 0:
			s.Note = fmt.Sprintf("%s%% left; no quote to set against it", pct(left))
		case w.USDPerPct <= 0:
			s.Note = fmt.Sprintf("%s%% left; no measured rate for this window yet, so no share", pct(left))
		default:
			p50Pct, p90Pct := p50/w.USDPerPct, p90/w.USDPerPct
			s.Known, s.P50Pct, s.P90Pct = true, round1(p50Pct), round1(p90Pct)
			s.FitsP90 = p90Pct <= left
			s.LeftAfterP90Pct = round1(math.Max(0, left-p90Pct))
			if s.FitsP90 {
				s.Note = fmt.Sprintf("about %s%% typical, %s%% at the p90, of the %s%% left", pct(s.P50Pct), pct(s.P90Pct), pct(left))
			} else {
				s.Note = fmt.Sprintf("about %s%% at the p90, more than the %s%% left", pct(s.P90Pct), pct(left))
			}
		}
		out = append(out, s)
	}
	return out
}

// quantile interpolates linearly between the sorted values.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	pos := q * float64(len(sorted)-1)
	lo, hi := int(math.Floor(pos)), int(math.Ceil(pos))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

func majority(counts map[string]int) string {
	best, n := "", 0
	for k, c := range counts {
		if c > n || (c == n && k < best) {
			best, n = k, c
		}
	}
	return best
}

func modelWords(m string) string {
	if strings.TrimSpace(m) == "" {
		return "this model"
	}
	return m
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func pct(v float64) string {
	v = round1(v)
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// durationWords renders a wait compactly: 45m, 1h10, 2d 3h.
func durationWords(d time.Duration) string {
	d = d.Round(time.Minute)
	days, hours, mins := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%02d", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

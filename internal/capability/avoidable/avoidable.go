// Package avoidable prices the waste taxonomy (#655): how much of the
// period's usage was avoidable, class by class, in both currencies an
// operator thinks in — API-equivalent dollars and the share of a plan
// window it took — beside what the coach's interventions are measured to
// have saved, and which guardrail to switch on next.
//
// Each class reads an existing source rather than a new heuristic: the
// read guard's ledger, the prompt cache counts each turn records, the
// coach's model advice, the waste detector, and the commit and outcome
// joins of cost per commit and per outcome. The classes overlap in what
// they look at, so each token is given to at most one of them
// (waste.Attribute). The report holds derived figures only: no session,
// path, prompt or repository reaches it.
package avoidable

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/effortweeks"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// DefaultDays is the period when none is asked for: a week, the cadence
// of the glance's headline.
const DefaultDays = 7

// CalibrationDays is how far before the period the plan windows that
// size a percent are read from, as unit costs read them.
const CalibrationDays = uniteconomics.BaselineDays

// Attribution says how classes share tokens, for every surface to show.
const Attribution = "each token goes to at most one class, in the order listed; dollars are each turn's API-equivalent value in proportion to its tokens"

// ShareMethod says how a window share is measured.
const ShareMethod = uniteconomics.ShareMethod

// Usage is a turn's tokens as a rate card prices them.
type Usage struct {
	Provider, Model                                    string
	Input, CacheRead, CacheWrite, CacheWrite1h, Output int64
}

// Pricer prices usage at the rate card in effect at a time; false when
// the model has no list price.
type Pricer func(u Usage, at time.Time) (float64, bool)

// Reread is one re-read from the read guard's ledger.
type Reread struct {
	At      time.Time
	Session string
	Tokens  int64
}

// RouteAdvice is the coach's advice to move a session's work to a
// cheaper model.
type RouteAdvice struct {
	At            time.Time
	Session, Kind string
	From, To      string
}

// Inputs is everything the report is built from: the pure half of
// Compute.
type Inputs struct {
	// Since and Until bound the period.
	Since, Until time.Time
	// Turns are the turns from CalibrationDays before Since to Until; the
	// classes read those in the period, the window sizes all of them.
	Turns []analytics.SessionTurn
	// Dirs, RootOf and CommitsOf are the commit join's inputs; Outcomes
	// the outcome events recorded.
	Dirs      map[string]sessiondirs.Dir
	RootOf    map[string]string
	CommitsOf map[string][]gitlog.Commit
	Outcomes  []*eventschema.Envelope
	// Rereads are the redundant re-reads the guard watched go ahead;
	// Refused those it refused.
	Rereads, Refused []Reread
	// Routes are the coach's model advice.
	Routes []RouteAdvice
	// Stops are the instructions the developer stopped, with the turns
	// each ran before the stop: the stopped-work class prices them.
	Stops []Stop
	// line is the context line the operator's bands measured, set by
	// gather with the override it applies to the waste detector.
	line *ContextLine
	// bands are the period's context bands, read with the line.
	bands []agentdx.ContextBand
	// Traces are the period's workflows by ID, for the waste detector,
	// configured by Waste.
	Traces map[string]*workflow.Trace
	Waste  waste.Config
	// Price reprices a turn; nil leaves the cache rebuilds and the
	// over-tiered model unpriced.
	Price Pricer
	// Lint reads the context lint for the prompt prefix changes' likely
	// causes, only for the projects those rebuilds ran in; nil names
	// none.
	Lint Linter
	// Windows are each provider's plan window readings.
	Windows map[string][]plans.QuotaWindow
	// Savings is the realized-savings ledger over the period; Steps each
	// guardrail's graduation.
	Savings savings.Report
	Steps   []savings.Step
	// Fmt are the command_fmt compressions recorded in the period, and
	// Holds the calls a boundary stopped: the prevented side of the report.
	Fmt   []FmtSaving
	Holds []Hold
	// Compaction is the weekly rollup the compaction setting is judged
	// on, nil when none is stored.
	Compaction *Weeks
	// Effort is each model's judged change of effort level (ADR 0022).
	Effort []effortweeks.Watch
}

// Stop is an instruction the developer stopped: its session, when the
// instruction was typed (From) and when it was stopped (At).
type Stop struct {
	Session  string
	From, At time.Time
}

// Concentration says how few sessions a class's dollars came from: the
// fewest that make up half of them, and their share.
type Concentration struct {
	// Sessions is how many sessions the class was found in.
	Sessions int `json:"sessions"`
	// Top is the fewest sessions whose dollars reach half of the class's.
	Top int `json:"top"`
	// TopPct is their share of the class's dollars, in percent.
	TopPct float64 `json:"top_pct"`
}

// WindowShare is an amount as a percentage of one provider's plan window.
type WindowShare struct {
	Provider string  `json:"provider"`
	Window   string  `json:"window"`
	Pct      float64 `json:"pct"`
}

// Amount is some usage in both currencies.
type Amount struct {
	Tokens           int64   `json:"tokens"`
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	// BilledUSD is the part a provider billed per use: usage stored with
	// a cost, or without one on a provider no plan's windows cover and
	// not marked plan-covered. On a flat-rate plan the rest cost nothing
	// beyond the plan, so its measure is WindowShare, not dollars.
	BilledUSD float64 `json:"billed_usd"`
	// WindowShare is the amount as a share of each calibrated plan
	// window; absent without a flat-rate plan's readings.
	WindowShare []WindowShare `json:"window_share,omitempty"`
}

// ClassCost is one class of the taxonomy, priced.
type ClassCost struct {
	Class waste.Class `json:"class"`
	Name  string      `json:"name"`
	Amount
	// Turns and Sessions count what the class was found in.
	Turns    int    `json:"turns"`
	Sessions int    `json:"sessions"`
	Method   string `json:"method"`
	// Guardrail is the capability that addresses the class; empty when
	// none does yet.
	Guardrail string `json:"guardrail,omitempty"`
	// Causes split the cache rebuilds by why each happened, largest
	// first; absent on every other class.
	Causes []CauseCost `json:"causes,omitempty"`
	// Fix is the setting or habit that prevents the class.
	Fix string `json:"fix,omitempty"`
	// Concentration says how many sessions carry the class's dollars;
	// absent when the class has no dollars.
	Concentration *Concentration `json:"concentration,omitempty"`
	// Inferred marks a class that reads waste into what did not happen
	// rather than observing it: a session with no commit and no outcome
	// may have been research, a review or a question. It is shown, and
	// left out of Avoidable, the headline and the guardrails' ranking.
	Inferred bool `json:"inferred,omitempty"`
}

// inferred are the classes that infer waste from an absence.
var inferred = map[waste.Class]bool{waste.ClassAbandoned: true}

// CauseCost is the part of the cache rebuilds one cause accounts for.
type CauseCost struct {
	Cause waste.RebuildCause `json:"cause"`
	Name  string             `json:"name"`
	Amount
	Turns int `json:"turns"`
	// TTL is, for idle gaps, the cache lifetime most of them outlived:
	// "5m" or "1h".
	TTL string `json:"ttl,omitempty"`
	// Likely are, for prompt prefix changes, the context lint's items
	// that plausibly explain them, joined as LikelyMethod says; absent
	// when none is found or the lint was not read.
	Likely       []LikelyCause `json:"likely,omitempty"`
	LikelyMethod string        `json:"likely_method,omitempty"`
}

// TopCause is the cause that accounts for most of the class's dollars;
// false when nothing is split by cause.
func (c ClassCost) TopCause() (CauseCost, bool) {
	if len(c.Causes) == 0 || c.Causes[0].Tokens <= 0 {
		return CauseCost{}, false
	}
	return c.Causes[0], true
}

// Realized is what the coach's interventions saved over the period.
type Realized struct {
	// HoldoutUSD is what the holdout comparisons measured; unknown while
	// none has concluded.
	HoldoutUSD measurement.Value `json:"holdout_usd"`
	// RefusedRereads are the re-reads the guard refused, RefusedTokens
	// their size and RefusedUSD their value at the session's rate (an
	// estimate).
	RefusedRereads int     `json:"refused_rereads"`
	RefusedTokens  int64   `json:"refused_tokens"`
	RefusedUSD     float64 `json:"refused_usd"`
	// USD is the two together.
	USD float64 `json:"usd"`
	// Share is USD of USD and the avoidable dollars: how much of what
	// could have been saved was. Absent when both are zero.
	Share *float64 `json:"share,omitempty"`
}

// ContextLine is the size the developer's own context bands measured.
type ContextLine struct {
	Tokens int64 `json:"tokens"`
	// Basis says how the line was read.
	Basis string `json:"basis"`
}

// lineBasis is how a derived context line is described.
const lineBasis = "the lower edge of your first context band (at least 50 instructions) whose rejections rise 2 points above your lowest band"

// Guardrail is one capability's case for being switched on next: the
// avoidable usage it addresses and where its graduation stands.
type Guardrail struct {
	Capability string        `json:"capability"`
	Name       string        `json:"name"`
	Power      string        `json:"power"`
	Classes    []waste.Class `json:"classes"`
	Amount
	Stage learning.Stage `json:"stage"`
	// Next is the stage it would move to; empty when it already acts.
	Next  learning.Stage `json:"next,omitempty"`
	Ready bool           `json:"ready"`
	// Promote is the command that takes the step; Bar the graduation
	// criteria with where the evidence stands.
	Promote string `json:"promote,omitempty"`
	Bar     string `json:"bar,omitempty"`
}

// Report is the period's avoidable usage.
type Report struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	// ContextLine is the context size past which the developer's own
	// bands show instructions going worse, when they show one; the
	// oversized-context class is read at it.
	ContextLine *ContextLine `json:"context_line,omitempty"`
	// Usage is everything the period used; Avoidable the part the
	// classes account for.
	Usage     Amount      `json:"usage"`
	Avoidable Amount      `json:"avoidable"`
	Classes   []ClassCost `json:"classes"`
	// Prevented is what the mechanisms stopped; found is Avoidable. They
	// sit side by side and are never added together.
	Prevented Prevented `json:"prevented"`
	Realized  Realized  `json:"realized"`
	// Guardrails rank the capabilities: ready first, then by what each
	// addresses; one that already acts comes last.
	Guardrails []Guardrail `json:"guardrails"`
	// Next is the guardrail to switch on next, when one addresses
	// anything.
	Next *Guardrail `json:"next,omitempty"`
	// Headline is the week in one line, for an ambient surface; empty
	// when nothing was avoidable.
	Headline     string                    `json:"headline,omitempty"`
	Attribution  string                    `json:"attribution"`
	Calibrations []plans.WindowCalibration `json:"calibrations,omitempty"`
	ShareMethod  string                    `json:"share_method,omitempty"`
	// Notes say what could not be priced.
	Notes []string `json:"notes,omitempty"`
}

var methods = map[waste.Class]string{
	waste.ClassReread: "the read guard's ledger: each unchanged file read again in full while its earlier read was in context, " +
		"which the guard watched go ahead, sized from the file, on the turn that carried it",
	waste.ClassCacheRebuild: "a turn that wrote to the prompt cache more than half of the prefix the turn before had cached, " +
		"without a compaction, priced as writing rather than reading that part at the rate card; " +
		"split by cause: a model switch, a prompt prefix change after cache hits, or unexplained",
	waste.ClassIdleRebuild: "the same rebuild after an idle gap longer than the cache lifetime (an hour when the session wrote with " +
		"the hour-long lifetime, else five minutes), priced as writing rather than reading that part at the rate card",
	waste.ClassStopped: "for each instruction the developer stopped, the turn in flight at the stop (sent before it, recorded after, " +
		"its send time known from the recorded latency), priced at the rate card: work the stop discarded",
	waste.ClassOverTiered: "turns left on a model after the coach advised a cheaper one for that kind of work, " +
		"priced as the difference to the advised model at the rate card",
	waste.ClassRetryLoop: "the waste detector on each workflow: an identical prompt sent again, an agent's steps past the loop limit, " +
		"and context growth above the per-step limit",
	waste.ClassOversized: "the waste detector on each workflow: context carried above the line a session should compact at, " +
		"or above the context ceiling",
	waste.ClassAbandoned: "sessions over for a day with no commit of yours within a day of any of their turns and no outcome recorded " +
		"(the joins of cost per commit and per outcome); a session outside a repository is not judged",
}

// guardrailOf is the capability that addresses each class.
var guardrailOf = map[waste.Class]string{
	waste.ClassReread:     learning.CapabilityReadGuard,
	waste.ClassOverTiered: learning.CapabilityRouting,
	waste.ClassOversized:  learning.CapabilityCompaction,
	waste.ClassAbandoned:  learning.CapabilitySessionBudget,
}

// TurnWaste is the part of one turn given to one class.
type TurnWaste struct {
	Turn             analytics.SessionTurn
	Class            waste.Class
	Tokens           int64
	APIEquivalentUSD float64
}

// Build computes the report from in.
func Build(in Inputs) Report {
	r, _ := BuildTurns(in)
	return r
}

// BuildTurns is Build with the attribution it sums: each period turn's
// avoidable tokens per class, oldest turn first, for a caller that files
// them under its own keys (the team upload files them by day, repository
// and kind of work).
func BuildTurns(in Inputs) (Report, []TurnWaste) {
	var turns []TurnWaste
	r := Report{Since: in.Since, Until: in.Until, Attribution: Attribution, Classes: []ClassCost{}, Guardrails: []Guardrail{}, ContextLine: in.line}
	b := newBook(in)
	b.claimRereads(in.Rereads)
	b.claimCacheRebuilds()
	b.claimOverTiered(in.Routes)
	b.claimDetector(in.Traces, in.Waste)
	b.claimStopped(in.Stops)
	b.claimAbandoned(in)

	attributed := waste.Attribute(b.budget(), b.claims)
	byClass := map[waste.Class]*ClassCost{}
	tokensOf := map[waste.Class]map[string]int64{}
	sessionsOf := map[waste.Class]map[string]bool{}
	for _, c := range waste.Classes() {
		cc := &ClassCost{Class: c, Name: waste.ClassName(c), Method: methods[c], Fix: waste.FixOf(c), Guardrail: guardrailOf[c], Inferred: inferred[c]}
		byClass[c], tokensOf[c], sessionsOf[c] = cc, map[string]int64{}, map[string]bool{}
	}
	causes := newCauseBook()
	likely := newLikelyBook(in)
	usageTokens := map[string]int64{}
	avoidTokens := map[string]int64{}
	r.Calibrations = calibrations(in)
	onPlan := map[string]bool{}
	for _, c := range r.Calibrations {
		onPlan[c.Provider] = true
	}
	for p, i := range b.period {
		t := in.Turns[i]
		r.Usage.Tokens += t.Tokens
		r.Usage.APIEquivalentUSD += t.APIEquivalentUSD
		usageTokens[t.Provider] += t.Tokens
		for _, c := range waste.Classes() {
			n := attributed[p][c]
			if n <= 0 {
				continue
			}
			cc := byClass[c]
			v := valueOf(t, n)
			turns = append(turns, TurnWaste{Turn: t, Class: c, Tokens: n, APIEquivalentUSD: v})
			cc.Tokens += n
			cc.APIEquivalentUSD += v
			cc.BilledUSD += billedOf(t, n, onPlan[t.Provider])
			cc.Turns++
			sessionsOf[c][t.SessionID] = true
			tokensOf[c][t.Provider] += n
			if !inferred[c] {
				avoidTokens[t.Provider] += n
			}
			if note, ok := b.rebuilds[p]; ok && waste.RebuildClass(note.cause) == c {
				causes.add(note, t, n, v)
				if note.cause == waste.CausePrefixChanged {
					likely.add(t)
				}
			}
		}
	}
	if len(r.Calibrations) > 0 {
		r.ShareMethod = ShareMethod
	}
	for _, c := range waste.Classes() {
		cc := byClass[c]
		cc.Sessions = len(sessionsOf[c])
		cc.WindowShare = shares(r.Calibrations, tokensOf[c])
		cc.Concentration = concentrationOf(turns, c)
		if c == waste.ClassCacheRebuild || c == waste.ClassIdleRebuild {
			cc.Causes = causes.costs(r.Calibrations, c)
			likely.attach(cc.Causes)
		}
		if !cc.Inferred {
			r.Avoidable.Tokens += cc.Tokens
			r.Avoidable.APIEquivalentUSD += cc.APIEquivalentUSD
			r.Avoidable.BilledUSD += cc.BilledUSD
		}
		r.Classes = append(r.Classes, *cc)
	}
	r.Usage.WindowShare = shares(r.Calibrations, usageTokens)
	r.Avoidable.WindowShare = shares(r.Calibrations, avoidTokens)
	r.Realized = realized(in, b, r.Avoidable.APIEquivalentUSD)
	r.Prevented = buildPrevented(in, r)
	r.Guardrails, r.Next = rank(in.Steps, r.Classes)
	r.Notes = b.notes()
	r.Headline = headline(r)
	return r, turns
}

// causeBook sums the cache rebuilds' attributed tokens by cause.
type causeBook struct {
	cost     map[waste.RebuildCause]*CauseCost
	tokensOf map[waste.RebuildCause]map[string]int64
	ttlOf    map[waste.RebuildCause]map[time.Duration]int64
}

func newCauseBook() *causeBook {
	return &causeBook{cost: map[waste.RebuildCause]*CauseCost{}, tokensOf: map[waste.RebuildCause]map[string]int64{},
		ttlOf: map[waste.RebuildCause]map[time.Duration]int64{}}
}

func (cb *causeBook) add(note rebuildNote, t analytics.SessionTurn, tokens int64, usd float64) {
	c, ok := cb.cost[note.cause]
	if !ok {
		c = &CauseCost{Cause: note.cause, Name: waste.RebuildCauseName(note.cause)}
		cb.cost[note.cause] = c
		cb.tokensOf[note.cause] = map[string]int64{}
		cb.ttlOf[note.cause] = map[time.Duration]int64{}
	}
	c.Tokens += tokens
	c.APIEquivalentUSD += usd
	c.Turns++
	cb.tokensOf[note.cause][t.Provider] += tokens
	cb.ttlOf[note.cause][note.ttl] += tokens
}

// costs are the causes of one class, largest first, each with its window
// share.
func (cb *causeBook) costs(cs []plans.WindowCalibration, class waste.Class) []CauseCost {
	var out []CauseCost
	for _, cause := range waste.RebuildCauses() {
		c, ok := cb.cost[cause]
		if !ok || waste.RebuildClass(cause) != class {
			continue
		}
		c.WindowShare = shares(cs, cb.tokensOf[cause])
		if cause == waste.CauseTTLExpired {
			c.TTL = ttlLabel(cb.ttlOf[cause])
		}
		out = append(out, *c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].APIEquivalentUSD > out[j].APIEquivalentUSD })
	return out
}

// ttlLabel is the lifetime that carried the most tokens.
func ttlLabel(by map[time.Duration]int64) string {
	var best time.Duration
	for d, n := range by {
		if n > by[best] || n == by[best] && d > best {
			best = d
		}
	}
	switch best {
	case waste.CacheTTLExtended:
		return "1h"
	case waste.CacheTTL:
		return "5m"
	}
	return ""
}

// valueOf is n of a turn's tokens at its API-equivalent rate.
func valueOf(t analytics.SessionTurn, n int64) float64 {
	if t.Tokens <= 0 {
		return 0
	}
	return t.APIEquivalentUSD * float64(n) / float64(t.Tokens)
}

// billedOf is what n of a turn's tokens were billed per use. A turn
// stored with a cost was billed it. One without a cost was billed its
// list price only when no plan covers it: a reader that leaves plan
// usage unmarked must not turn a subscription into spend.
func billedOf(t analytics.SessionTurn, n int64, onPlan bool) float64 {
	if t.Tokens <= 0 || t.PlanCovered || (t.CostUSD <= 0 && onPlan) {
		return 0
	}
	return t.BilledUSD() * float64(n) / float64(t.Tokens)
}

// calibrations size each provider's plan window from the turns recorded
// while its windows were open.
func calibrations(in Inputs) []plans.WindowCalibration {
	byProvider := map[string]*tokenSeries{}
	for _, t := range in.Turns {
		if _, ok := in.Windows[t.Provider]; !ok {
			continue
		}
		s := byProvider[t.Provider]
		if s == nil {
			s = &tokenSeries{}
			byProvider[t.Provider] = s
		}
		s.at = append(s.at, t.At)
		s.tokens = append(s.tokens, t.Tokens)
	}
	providers := make([]string, 0, len(in.Windows))
	for p := range in.Windows {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	var out []plans.WindowCalibration
	for _, p := range providers {
		s := byProvider[p]
		if s == nil {
			s = &tokenSeries{}
		}
		s.index()
		since := in.Since.AddDate(0, 0, -CalibrationDays)
		if c, ok := plans.Calibrate(p, in.Windows[p], since, in.Until, s.in); ok {
			out = append(out, c)
		}
	}
	return out
}

// tokenSeries is one provider's turns as times and tokens, for summing
// the tokens of many windows. Each day of a report sized every window by
// copying every turn and scanning them all for each window: on a busy
// machine the largest share of what the daemon allocated.
type tokenSeries struct {
	at     []time.Time
	tokens []int64
	// sum[i] is the tokens of the first i turns in time order.
	sum []int64
}

// index orders the turns by time and sums them.
func (s *tokenSeries) index() {
	sort.Sort(byAt{s})
	s.sum = make([]int64, len(s.at)+1)
	for i, n := range s.tokens {
		s.sum[i+1] = s.sum[i] + n
	}
}

// in is the tokens of the turns at or after from and before to.
func (s *tokenSeries) in(from, to time.Time) int64 {
	lo := sort.Search(len(s.at), func(i int) bool { return !s.at[i].Before(from) })
	hi := sort.Search(len(s.at), func(i int) bool { return !s.at[i].Before(to) })
	if hi <= lo {
		return 0
	}
	return s.sum[hi] - s.sum[lo]
}

type byAt struct{ s *tokenSeries }

func (b byAt) Len() int           { return len(b.s.at) }
func (b byAt) Less(i, j int) bool { return b.s.at[i].Before(b.s.at[j]) }
func (b byAt) Swap(i, j int) {
	b.s.at[i], b.s.at[j] = b.s.at[j], b.s.at[i]
	b.s.tokens[i], b.s.tokens[j] = b.s.tokens[j], b.s.tokens[i]
}

// shares states each provider's tokens as a share of its window.
func shares(cs []plans.WindowCalibration, tokens map[string]int64) []WindowShare {
	var out []WindowShare
	for _, c := range cs {
		if n := tokens[c.Provider]; n > 0 {
			out = append(out, WindowShare{Provider: c.Provider, Window: c.Window, Pct: c.Share(n)})
		}
	}
	return out
}

// realized is what the period's interventions saved, beside avoidable.
func realized(in Inputs, b *book, avoidableUSD float64) Realized {
	r := Realized{HoldoutUSD: in.Savings.RealizedUSD}
	for _, rr := range in.Refused {
		if rr.At.Before(in.Since) || !rr.At.Before(in.Until) || rr.Tokens <= 0 {
			continue
		}
		r.RefusedRereads++
		r.RefusedTokens += rr.Tokens
		r.RefusedUSD += float64(rr.Tokens) * b.rateOf(rr.Session)
	}
	r.USD = r.RefusedUSD
	if v, ok := in.Savings.RealizedUSD.Amount(); ok {
		r.USD += v
	}
	if total := r.USD + avoidableUSD; total > 0 {
		s := r.USD / total
		r.Share = &s
	}
	return r
}

// rank orders the guardrails: those that can still step up before those
// that already act, ready before not, then by the avoidable dollars they
// address. Next is the first that can step up and addresses anything.
func rank(steps []savings.Step, classes []ClassCost) ([]Guardrail, *Guardrail) {
	out := make([]Guardrail, 0, len(steps))
	for _, s := range steps {
		g := Guardrail{Capability: s.Capability, Name: savings.CapabilityName(s.Capability), Power: s.Power,
			Classes: []waste.Class{}, Stage: s.Stage, Next: s.Next, Ready: s.Ready, Promote: s.Promote}
		if s.Next != "" {
			g.Bar = savings.Bar(s)
		}
		for _, c := range classes {
			if c.Guardrail != s.Capability || c.Inferred {
				continue
			}
			g.Classes = append(g.Classes, c.Class)
			g.Tokens += c.Tokens
			g.APIEquivalentUSD += c.APIEquivalentUSD
		}
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Next == "") != (b.Next == "") {
			return b.Next == ""
		}
		if a.Ready != b.Ready {
			return a.Ready
		}
		return a.APIEquivalentUSD > b.APIEquivalentUSD
	})
	for i := range out {
		if out[i].Next != "" && out[i].Tokens > 0 {
			next := out[i]
			return out, &next
		}
	}
	return out, nil
}

// headline is the period in one line: what was prevented, beside what is
// still found.
func headline(r Report) string {
	if r.Avoidable.Tokens <= 0 && !r.Prevented.Active() {
		return ""
	}
	days := int(math.Round(r.Until.Sub(r.Since).Hours() / 24))
	var b strings.Builder
	fmt.Fprintf(&b, "Last %d days: prevented %s", days, r.Prevented.Words())
	if r.Avoidable.Tokens > 0 {
		fmt.Fprintf(&b, "; still found ~%s avoidable", dollars(r.Avoidable.APIEquivalentUSD))
		if s, ok := largest(r.Avoidable.WindowShare); ok {
			fmt.Fprintf(&b, " (%.1f%% of a %s window)", s.Pct, s.Window)
		}
		if top := topClass(r.Classes); top != "" {
			fmt.Fprintf(&b, ", most of it %s", top)
		}
	}
	if n := r.Next; n != nil {
		if n.Ready {
			fmt.Fprintf(&b, ". Next guardrail: %s, ready to %s", n.Name, n.Next)
		} else {
			fmt.Fprintf(&b, ". Next guardrail: %s, still gathering evidence", n.Name)
		}
	}
	return b.String()
}

// Active reports whether any mechanism acted in the period, priced or not.
func (p Prevented) Active() bool {
	for _, m := range p.Mechanisms {
		if m.Count > 0 {
			return true
		}
	}
	return false
}

// Priced reports whether an active mechanism has dollars the report can
// state.
func (p Prevented) Priced() bool {
	for _, m := range p.Mechanisms {
		if _, ok := m.USD.Amount(); ok && m.Count > 0 {
			return true
		}
	}
	return false
}

// Words is the prevented total in a sentence fragment: the dollars, or
// that it is not priced yet, or that nothing was prevented.
func (p Prevented) Words() string {
	switch {
	case !p.Active():
		return "none in the period"
	case !p.Priced():
		return "not priced yet"
	}
	return "~" + dollars(p.USD)
}

func largest(ss []WindowShare) (WindowShare, bool) {
	var best WindowShare
	for _, s := range ss {
		if s.Pct > best.Pct {
			best = s
		}
	}
	return best, best.Pct > 0
}

func topClass(cs []ClassCost) string {
	var best ClassCost
	for _, c := range cs {
		if !c.Inferred && c.APIEquivalentUSD > best.APIEquivalentUSD {
			best = c
		}
	}
	if best.APIEquivalentUSD <= 0 {
		return ""
	}
	return waste.ClassName(best.Class)
}

func dollars(v float64) string {
	if v > 0 && v < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}

// concentrationOf is how many sessions carry a class's dollars: the fewest
// whose dollars reach half, and their share. Nil when the class has none.
func concentrationOf(turns []TurnWaste, c waste.Class) *Concentration {
	usd := map[string]float64{}
	var total float64
	for _, t := range turns {
		if t.Class != c || t.APIEquivalentUSD <= 0 {
			continue
		}
		usd[t.Turn.SessionID] += t.APIEquivalentUSD
		total += t.APIEquivalentUSD
	}
	if total <= 0 {
		return nil
	}
	per := make([]float64, 0, len(usd))
	for _, v := range usd {
		per = append(per, v)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(per)))
	var acc float64
	top := 0
	for _, v := range per {
		acc += v
		top++
		if acc*2 >= total {
			break
		}
	}
	return &Concentration{Sessions: len(per), Top: top, TopPct: math.Round(acc/total*1000) / 10}
}

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

	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
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
}

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
	// Usage is everything the period used; Avoidable the part the
	// classes account for.
	Usage     Amount      `json:"usage"`
	Avoidable Amount      `json:"avoidable"`
	Classes   []ClassCost `json:"classes"`
	Realized  Realized    `json:"realized"`
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
		"split by cause: a model switch, an idle gap past the cache lifetime (an hour when the session wrote with the hour-long lifetime, " +
		"else five minutes), a prompt prefix change after cache hits, or unexplained",
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
	r := Report{Since: in.Since, Until: in.Until, Attribution: Attribution, Classes: []ClassCost{}, Guardrails: []Guardrail{}}
	b := newBook(in)
	b.claimRereads(in.Rereads)
	b.claimCacheRebuilds()
	b.claimOverTiered(in.Routes)
	b.claimDetector(in.Traces, in.Waste)
	b.claimAbandoned(in)

	attributed := waste.Attribute(b.budget(), b.claims)
	byClass := map[waste.Class]*ClassCost{}
	tokensOf := map[waste.Class]map[string]int64{}
	sessionsOf := map[waste.Class]map[string]bool{}
	for _, c := range waste.Classes() {
		cc := &ClassCost{Class: c, Name: waste.ClassName(c), Method: methods[c], Guardrail: guardrailOf[c]}
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
			avoidTokens[t.Provider] += n
			if note, ok := b.rebuilds[p]; ok && c == waste.ClassCacheRebuild {
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
		if c == waste.ClassCacheRebuild {
			cc.Causes = causes.costs(r.Calibrations)
			likely.attach(cc.Causes)
		}
		r.Avoidable.Tokens += cc.Tokens
		r.Avoidable.APIEquivalentUSD += cc.APIEquivalentUSD
		r.Avoidable.BilledUSD += cc.BilledUSD
		r.Classes = append(r.Classes, *cc)
	}
	r.Usage.WindowShare = shares(r.Calibrations, usageTokens)
	r.Avoidable.WindowShare = shares(r.Calibrations, avoidTokens)
	r.Realized = realized(in, b, r.Avoidable.APIEquivalentUSD)
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

// costs are the causes, largest first, each with its window share.
func (cb *causeBook) costs(cs []plans.WindowCalibration) []CauseCost {
	var out []CauseCost
	for _, cause := range waste.RebuildCauses() {
		c, ok := cb.cost[cause]
		if !ok {
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
	byProvider := map[string][]analytics.SessionTurn{}
	for _, t := range in.Turns {
		byProvider[t.Provider] = append(byProvider[t.Provider], t)
	}
	providers := make([]string, 0, len(in.Windows))
	for p := range in.Windows {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	var out []plans.WindowCalibration
	for _, p := range providers {
		turns := byProvider[p]
		tokensIn := func(from, to time.Time) int64 {
			var n int64
			for _, t := range turns {
				if !t.At.Before(from) && t.At.Before(to) {
					n += t.Tokens
				}
			}
			return n
		}
		since := in.Since.AddDate(0, 0, -CalibrationDays)
		if c, ok := plans.Calibrate(p, in.Windows[p], since, in.Until, tokensIn); ok {
			out = append(out, c)
		}
	}
	return out
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
			if c.Guardrail != s.Capability {
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

// headline is the period in one line.
func headline(r Report) string {
	if r.Avoidable.Tokens <= 0 {
		return ""
	}
	days := int(math.Round(r.Until.Sub(r.Since).Hours() / 24))
	var b strings.Builder
	fmt.Fprintf(&b, "Last %d days: ~%s avoidable", days, dollars(r.Avoidable.APIEquivalentUSD))
	if s, ok := largest(r.Avoidable.WindowShare); ok {
		fmt.Fprintf(&b, " (%.1f%% of a %s window)", s.Pct, s.Window)
	}
	if top := topClass(r.Classes); top != "" {
		fmt.Fprintf(&b, ", most of it %s", top)
	}
	fmt.Fprintf(&b, "; %s realized", dollars(r.Realized.USD))
	if n := r.Next; n != nil {
		if n.Ready {
			fmt.Fprintf(&b, ". Next guardrail: %s, ready to %s", n.Name, n.Next)
		} else {
			fmt.Fprintf(&b, ". Next guardrail: %s, still gathering evidence", n.Name)
		}
	}
	return b.String()
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
		if c.APIEquivalentUSD > best.APIEquivalentUSD {
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

package avoidable

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/savings"
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

var t0 = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// turn is a turn of 1,000 tokens worth $1 at list price.
func turn(session string, at time.Duration) analytics.SessionTurn {
	return analytics.SessionTurn{SessionID: session, At: t0.Add(at), Provider: "anthropic", Model: "claude-opus-4-7",
		Tokens: 1_000, InputTokens: 900, OutputTokens: 100, APIEquivalentUSD: 1, Priced: true}
}

// price is a toy rate card: opus input $10/M, haiku $1/M, cache reads a
// tenth of input and writes 1.25×, output 5× input.
func price(u Usage, _ time.Time) (float64, bool) {
	rate := map[string]float64{"claude-opus-4-7": 10, "claude-haiku-4-5": 1}[u.Model]
	if rate == 0 {
		return 0, false
	}
	plain := u.Input - u.CacheRead - u.CacheWrite
	return (float64(plain)*rate + float64(u.CacheRead)*rate/10 + float64(u.CacheWrite)*rate*1.25 +
		float64(u.Output)*rate*5) / 1e6, true
}

func fixture() Inputs {
	// Session "lost" ran in a repository, never committed and recorded no
	// outcome: abandoned. One of its turns rebuilt its cache, another
	// carried a re-read the guard watched go ahead.
	lost := []analytics.SessionTurn{turn("lost", 0), turn("lost", time.Minute), turn("lost", 20*time.Minute)}
	lost[1].CacheReadTokens, lost[1].CacheWriteTokens = 100, 600
	lost[2].CacheReadTokens, lost[2].CacheWriteTokens = 50, 700 // most of the 700-token prefix rebuilt
	// Session "kept" committed. The coach advised moving its lookups to
	// haiku and two turns after the advice stayed on opus.
	kept := []analytics.SessionTurn{turn("kept", day), turn("kept", day+time.Minute), turn("kept", day+2*time.Minute)}
	// Session "loop" committed too; its workflow sent one prompt twice.
	loop := []analytics.SessionTurn{turn("loop", 2*day), turn("loop", 2*day+time.Minute)}
	for i := range loop {
		loop[i].WorkflowID = "wf-loop"
	}
	trace := &workflow.Trace{WorkflowID: "wf-loop"}
	for i, tn := range loop {
		trace.Steps = append(trace.Steps, workflow.Step{Index: i,
			Envelope: &eventschema.Envelope{Timestamp: tn.At},
			Prompt:   &eventschema.PromptEvent{PromptHash: "same", InputTokens: tn.InputTokens}})
	}
	trace.StepCount = len(trace.Steps)
	turns := append(append(append([]analytics.SessionTurn{}, lost...), kept...), loop...)
	return Inputs{
		Since: t0, Until: t0.Add(7 * day),
		Turns:     turns,
		Dirs:      map[string]sessiondirs.Dir{"lost": {CWD: "/w/app"}, "kept": {CWD: "/w/app"}, "loop": {CWD: "/w/app"}},
		RootOf:    map[string]string{"/w/app": "/w/app"},
		CommitsOf: map[string][]gitlog.Commit{"/w/app": {{SHA: "1", At: t0.Add(day + time.Hour)}, {SHA: "2", At: t0.Add(2*day + time.Hour)}}},
		Rereads:   []Reread{{At: t0.Add(30 * time.Second), Session: "lost", Tokens: 200}},
		Refused:   []Reread{{At: t0.Add(day), Session: "kept", Tokens: 500}},
		Routes:    []RouteAdvice{{At: t0.Add(day + 30*time.Second), Session: "kept", Kind: "lookup", From: "claude-opus-4-7", To: "claude-haiku-4-5"}},
		Traces:    map[string]*workflow.Trace{"wf-loop": trace},
		Price:     price,
		Windows: map[string][]plans.QuotaWindow{"anthropic": {
			// A weekly window that reset in the period and held 8 turns'
			// 8,000 tokens at 8%: 1,000 tokens per percent.
			{Label: "weekly", UsedPct: 8, ResetsAt: t0.Add(7*day - time.Hour), Duration: 7 * day},
		}},
		Savings: savings.Report{RealizedUSD: measurement.Unknown("no holdout comparison has concluded yet")},
	}
}

func class(r Report, c waste.Class) ClassCost {
	for _, cc := range r.Classes {
		if cc.Class == c {
			return cc
		}
	}
	return ClassCost{}
}

func TestBuildPricesEachClassInBothCurrencies(t *testing.T) {
	r := Build(fixture())
	if r.Usage.Tokens != 8_000 || r.Usage.APIEquivalentUSD != 8 {
		t.Fatalf("usage = %+v", r.Usage)
	}
	if got := class(r, waste.ClassReread); got.Tokens != 200 || math.Abs(got.APIEquivalentUSD-0.2) > 1e-9 {
		t.Errorf("reread = %+v", got)
	}
	// Turn 3 of "lost" rebuilt 650 of a 700-token prefix; at a write
	// costing 12.5× a read that is most of what the turn cost. Turn 2's
	// prefix was mostly read, so it is no rebuild.
	rebuild := class(r, waste.ClassCacheRebuild)
	if rebuild.Tokens <= 0 || rebuild.Turns != 1 {
		t.Errorf("cache rebuild = %+v", rebuild)
	}
	// Two opus turns after the advice; at a tenth of the price on haiku,
	// 90% of each was avoidable.
	if got := class(r, waste.ClassOverTiered); got.Tokens != 1_800 || got.Turns != 2 {
		t.Errorf("over-tiered = %+v", got)
	}
	// The repeated prompt's second send.
	if got := class(r, waste.ClassRetryLoop); got.Tokens != 1_000 || got.Turns != 1 {
		t.Errorf("retry loop = %+v", got)
	}
	// What is left of "lost": its three turns less the re-read and the
	// rebuild.
	if got := class(r, waste.ClassAbandoned); got.Tokens != 3_000-200-rebuild.Tokens || got.Sessions != 1 {
		t.Errorf("abandoned = %+v", got)
	}
	var sum int64
	for _, c := range r.Classes {
		sum += c.Tokens
		if c.Method == "" || c.Name == "" {
			t.Errorf("%s has no method or name", c.Class)
		}
	}
	if sum != r.Avoidable.Tokens || r.Avoidable.Tokens > r.Usage.Tokens {
		t.Errorf("avoidable %d, classes sum %d, usage %d", r.Avoidable.Tokens, sum, r.Usage.Tokens)
	}
	// 1,000 tokens per percent of the weekly window.
	if len(r.Avoidable.WindowShare) != 1 || math.Abs(r.Avoidable.WindowShare[0].Pct-float64(sum)/1_000) > 1e-9 {
		t.Errorf("window share = %+v", r.Avoidable.WindowShare)
	}
	if len(r.Calibrations) != 1 || r.ShareMethod == "" {
		t.Errorf("calibrations = %+v", r.Calibrations)
	}
}

// However many classes claim the same tokens, each goes to one class:
// the total never passes what was used.
func TestBuildNeverCountsATokenTwice(t *testing.T) {
	in := fixture()
	// Every turn of "lost" re-read, rebuilt and over-tiered as well.
	for _, at := range []time.Duration{0, time.Minute, 20 * time.Minute} {
		in.Rereads = append(in.Rereads, Reread{At: t0.Add(at), Session: "lost", Tokens: 5_000})
	}
	in.Routes = append(in.Routes, RouteAdvice{At: t0, Session: "lost", From: "claude-opus-4-7", To: "claude-haiku-4-5"})
	r := Build(in)
	var lost int64
	for _, c := range r.Classes {
		if c.Tokens < 0 {
			t.Fatalf("%s negative", c.Class)
		}
		lost += c.Tokens
	}
	if lost > r.Usage.Tokens {
		t.Fatalf("avoidable %d of %d used", lost, r.Usage.Tokens)
	}
	if got := class(r, waste.ClassReread); got.Tokens != 3_000 {
		t.Errorf("re-reads take what they claim first, capped at the turns: %+v", got)
	}
	if got := class(r, waste.ClassAbandoned); got.Tokens != 0 {
		t.Errorf("nothing of the abandoned session is left: %+v", got)
	}
}

func TestRealizedSitsBesideAvoidable(t *testing.T) {
	in := fixture()
	in.Savings.RealizedUSD = measurement.Derived(1.5, "holdout")
	r := Build(in)
	// 500 refused tokens in "kept", whose turns run at $1 per 1,000.
	if r.Realized.RefusedRereads != 1 || r.Realized.RefusedTokens != 500 || math.Abs(r.Realized.RefusedUSD-0.5) > 1e-9 {
		t.Errorf("refused = %+v", r.Realized)
	}
	if math.Abs(r.Realized.USD-2) > 1e-9 {
		t.Errorf("realized = %v, want 2", r.Realized.USD)
	}
	want := 2 / (2 + r.Avoidable.APIEquivalentUSD)
	if r.Realized.Share == nil || math.Abs(*r.Realized.Share-want) > 1e-9 {
		t.Errorf("share = %v, want %v", r.Realized.Share, want)
	}
}

func step(capability, power string, stage learning.Stage, ready bool) savings.Step {
	g := learning.Graduation{Capability: capability, Stage: stage, Ready: ready}
	switch stage {
	case learning.StageShadow:
		g.Next = learning.StageAdvise
	case learning.StageAdvise:
		g.Next = learning.StageAct
	}
	return savings.Step{Graduation: g, Power: power, Promote: "tokenops coach set " + power + " next"}
}

// The next guardrail is the one whose evidence is ready and that would
// address the most; one not yet ready ranks after it whatever it would
// save, and one already acting is not offered.
func TestGuardrailsRankByReadinessThenAvoidable(t *testing.T) {
	in := fixture()
	in.Steps = []savings.Step{
		step(learning.CapabilityReadGuard, "waste", learning.StageShadow, true),
		step(learning.CapabilityRouting, "models", learning.StageAdvise, false),
		step(learning.CapabilitySessionBudget, "inform", learning.StageAct, false),
		step(learning.CapabilityCompaction, "context", learning.StageShadow, false),
	}
	r := Build(in)
	if r.Next == nil || r.Next.Capability != learning.CapabilityReadGuard {
		t.Fatalf("next = %+v", r.Next)
	}
	if !strings.Contains(r.Next.Promote, "coach set waste") {
		t.Errorf("promote = %q", r.Next.Promote)
	}
	order := make([]string, 0, len(r.Guardrails))
	for _, g := range r.Guardrails {
		order = append(order, g.Capability)
	}
	// Routing would address the over-tiered $1.80 but is not ready; the
	// session budget already acts; compaction addresses nothing here.
	if strings.Join(order, ",") != "read_guard,routing,compaction,session_budget" {
		t.Errorf("order = %v", order)
	}
	if !strings.Contains(r.Headline, "avoidable") || !strings.Contains(r.Headline, "read guard") {
		t.Errorf("headline = %q", r.Headline)
	}
}

func TestBuildWithNothingRecorded(t *testing.T) {
	r := Build(Inputs{Since: t0, Until: t0.Add(day)})
	if r.Avoidable.Tokens != 0 || len(r.Classes) != len(waste.Classes()) || r.Next != nil || r.Realized.Share != nil {
		t.Errorf("empty report = %+v", r)
	}
	if r.Headline != "" {
		t.Errorf("headline with nothing to say: %q", r.Headline)
	}
}

// The report is derived figures: no session IDs, paths or model text
// beyond the classes' fixed vocabulary.
func TestReportCarriesNoIdentifiers(t *testing.T) {
	b, err := json.Marshal(Build(fixture()))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"lost", "kept", "wf-loop", "/w/app"} {
		if strings.Contains(string(b), `"`+leak) {
			t.Errorf("report carries %q: %s", leak, b)
		}
	}
}

// BuildTurns hands out the attribution the report sums: per class, the
// turns' tokens and dollars add up to the class's.
func TestBuildTurnsSumToTheClasses(t *testing.T) {
	r, turns := BuildTurns(fixture())
	if len(turns) == 0 {
		t.Fatal("no attributed turns")
	}
	tokens, usd := map[waste.Class]int64{}, map[waste.Class]float64{}
	for i, tw := range turns {
		if i > 0 && tw.Turn.At.Before(turns[i-1].Turn.At) {
			t.Errorf("turns out of order at %d", i)
		}
		tokens[tw.Class] += tw.Tokens
		usd[tw.Class] += tw.APIEquivalentUSD
	}
	for _, cc := range r.Classes {
		if tokens[cc.Class] != cc.Tokens || math.Abs(usd[cc.Class]-cc.APIEquivalentUSD) > 1e-9 {
			t.Errorf("%s: turns hold %d tokens $%.4f, class %d $%.4f", cc.Class, tokens[cc.Class], usd[cc.Class], cc.Tokens, cc.APIEquivalentUSD)
		}
	}
}

// Avoidable usage on a flat-rate plan cost nothing more than the plan:
// only what a provider billed per use is money spent. Usage stored with
// a cost was billed; usage without one was billed only where no plan's
// windows cover the provider and the reader did not mark it plan-covered.
func TestBilledIsOnlyWhatWasBilledPerUse(t *testing.T) {
	if r := Build(fixture()); r.Avoidable.BilledUSD != 0 || r.Avoidable.APIEquivalentUSD <= 0 {
		t.Fatalf("plan usage without a stored cost counted as billed: %+v", r.Avoidable)
	}

	// "lost" ran on a pay-as-you-go provider that billed $2 per 1,000
	// tokens; all 3,000 of its tokens were avoidable.
	in := fixture()
	for i := range in.Turns {
		if in.Turns[i].SessionID == "lost" {
			in.Turns[i].Provider, in.Turns[i].CostUSD = "fireworks", 2
		}
	}
	r := Build(in)
	if math.Abs(r.Avoidable.BilledUSD-6) > 1e-9 {
		t.Errorf("billed = %v, want 6", r.Avoidable.BilledUSD)
	}
	var classes float64
	for _, c := range r.Classes {
		classes += c.BilledUSD
	}
	if math.Abs(classes-r.Avoidable.BilledUSD) > 1e-9 {
		t.Errorf("classes bill %v, total %v", classes, r.Avoidable.BilledUSD)
	}
	if got := class(r, waste.ClassOverTiered); got.BilledUSD != 0 {
		t.Errorf("plan session billed: %+v", got)
	}

	// Without any plan's windows, usage with no stored cost is metered at
	// list price, unless the reader marked it plan-covered.
	in = fixture()
	in.Windows = nil
	if r := Build(in); math.Abs(r.Avoidable.BilledUSD-r.Avoidable.APIEquivalentUSD) > 1e-9 {
		t.Errorf("metered usage: billed %v, list %v", r.Avoidable.BilledUSD, r.Avoidable.APIEquivalentUSD)
	}
	for i := range in.Turns {
		in.Turns[i].PlanCovered = true
	}
	if r := Build(in); r.Avoidable.BilledUSD != 0 {
		t.Errorf("plan-covered usage billed %v", r.Avoidable.BilledUSD)
	}
}

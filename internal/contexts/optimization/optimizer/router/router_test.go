package router

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func bodyWithModel(t *testing.T, model string, extra map[string]any) []byte {
	t.Helper()
	m := map[string]any{"model": model, "messages": []any{}}
	for k, v := range extra {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestRouteFromExpensiveToCheapEmitsSavings(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o", ToModel: "gpt-4o-mini", Quality: 0.9},
	}}, spend.NewEngine(spend.DefaultTable()))

	body := bodyWithModel(t, "gpt-4o", nil)
	recs, err := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o", Body: body,
		InputTokens: 1_000_000, OutputTokens: 500_000,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs = %d", len(recs))
	}
	rec := recs[0]
	if !strings.Contains(rec.Reason, "gpt-4o -> gpt-4o-mini") {
		t.Errorf("reason: %q", rec.Reason)
	}
	if rec.QualityScore != 0.9 {
		t.Errorf("quality lost: %f", rec.QualityScore)
	}
	if rec.EstimatedSavingsUSD <= 0 {
		t.Errorf("expected positive USD savings, got %f", rec.EstimatedSavingsUSD)
	}
	// Verify rewritten body switches model field.
	var got map[string]any
	if err := json.Unmarshal(rec.ApplyBody, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["model"] != "gpt-4o-mini" {
		t.Errorf("model not rewritten: %v", got["model"])
	}
}

func TestPrefixRuleMatches(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o*", ToModel: "gpt-4o-mini", Quality: 0.9},
	}}, nil)
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o-2026-01",
		Body: bodyWithModel(t, "gpt-4o-2026-01", nil),
	})
	if len(recs) != 1 {
		t.Fatalf("prefix should match, got %d", len(recs))
	}
}

func TestNoMatchEmitsNothing(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o", ToModel: "gpt-4o-mini", Quality: 0.9},
	}}, nil)
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-3.5-turbo",
		Body: bodyWithModel(t, "gpt-3.5-turbo", nil),
	})
	if len(recs) != 0 {
		t.Errorf("expected no rec, got %+v", recs)
	}
}

func TestQualityBelowMinSilenced(t *testing.T) {
	r := New(Config{
		MinQuality: 0.8,
		Rules: []Rule{
			{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o", ToModel: "gpt-4o-mini", Quality: 0.5},
		},
	}, nil)
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Body: bodyWithModel(t, "gpt-4o", nil),
	})
	if len(recs) != 0 {
		t.Errorf("low-quality rule should be silenced, got %+v", recs)
	}
}

func TestFallbackChain(t *testing.T) {
	r := New(Config{
		IsAvailable: func(_ eventschema.Provider, model string) bool {
			return model == "gpt-3.5-turbo" // primary unavailable, fallback ok
		},
		Rules: []Rule{
			{
				Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o",
				ToModel: "gpt-4o-mini", Fallbacks: []string{"o1", "gpt-3.5-turbo"},
				Quality: 0.85,
			},
		},
	}, nil)
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Body: bodyWithModel(t, "gpt-4o", nil),
	})
	if len(recs) != 1 {
		t.Fatalf("expected fallback rec, got %d", len(recs))
	}
	if !strings.Contains(recs[0].Reason, "-> gpt-3.5-turbo") {
		t.Errorf("expected fallback to gpt-3.5-turbo, got reason %q", recs[0].Reason)
	}
}

func TestAllTargetsUnavailable(t *testing.T) {
	r := New(Config{
		IsAvailable: func(eventschema.Provider, string) bool { return false },
		Rules: []Rule{
			{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o",
				ToModel: "gpt-4o-mini", Quality: 0.9},
		},
	}, nil)
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Body: bodyWithModel(t, "gpt-4o", nil),
	})
	if len(recs) != 1 {
		t.Fatalf("expected misroute rec, got %d", len(recs))
	}
	if recs[0].ApplyBody != nil {
		t.Errorf("ApplyBody should be nil when no target available: %q", recs[0].ApplyBody)
	}
	if !strings.Contains(recs[0].Reason, "no available target") {
		t.Errorf("reason: %q", recs[0].Reason)
	}
}

func TestRouteToMoreExpensiveReportsZeroSavings(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o-mini", ToModel: "gpt-4o", Quality: 0.99},
	}}, spend.NewEngine(spend.DefaultTable()))
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o-mini",
		InputTokens: 1_000_000, OutputTokens: 100_000,
		Body: bodyWithModel(t, "gpt-4o-mini", nil),
	})
	if len(recs) != 1 {
		t.Fatalf("expected rec")
	}
	if recs[0].EstimatedSavingsUSD != 0 {
		t.Errorf("upgrade should not report savings, got %f", recs[0].EstimatedSavingsUSD)
	}
}

func TestPreservesTopLevelFields(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o", ToModel: "gpt-4o-mini", Quality: 0.9},
	}}, nil)
	body := bodyWithModel(t, "gpt-4o", map[string]any{
		"temperature": 0.7,
		"tool_choice": "auto",
	})
	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o", Body: body,
	})
	if len(recs) != 1 {
		t.Fatalf("rec missing")
	}
	var got map[string]any
	_ = json.Unmarshal(recs[0].ApplyBody, &got)
	if got["temperature"] != 0.7 || got["tool_choice"] != "auto" {
		t.Errorf("fields lost: %v", got)
	}
}

func TestNilRequestNoOp(t *testing.T) {
	r := New(Config{}, nil)
	recs, err := r.Run(context.Background(), nil)
	if err != nil || len(recs) != 0 {
		t.Errorf("nil req: %v / %+v", err, recs)
	}
}

func TestKindIsRouter(t *testing.T) {
	r := New(Config{}, nil)
	if got := r.Kind(); got != eventschema.OptimizationTypeRouter {
		t.Errorf("kind = %s", got)
	}
}

// --- token-first accounting ---------------------------------------------

// A lateral route (same rate card on both sides) still moved real tokens.
// Reporting zero tokens because the dollar delta was zero conflates two
// independent measurements — and starves TEU, which sums this field.
func TestLateralRouteStillReportsTokens(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-opus-4-7", Quality: 0.9},
	}}, spend.NewEngine(spend.DefaultTable()))

	recs, err := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body:        bodyWithModel(t, "claude-opus-4-8", nil),
		InputTokens: 900_000, OutputTokens: 100_000,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs = %d", len(recs))
	}
	if got := recs[0].EstimatedSavingsTokens; got != 1_000_000 {
		t.Errorf("EstimatedSavingsTokens = %d, want 1000000 (tokens must not be gated on a USD delta)", got)
	}
	if got := recs[0].EstimatedSavingsUSD; got != 0 {
		t.Errorf("EstimatedSavingsUSD = %f, want 0 for a same-price route", got)
	}
}

// Routing to a pricier model is a real decision an operator may make for
// quality. It must not silently erase the token accounting.
func TestUpgradeRouteReportsTokensAndNoNegativeUSD(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderAnthropic, FromModel: "claude-haiku-4-5",
			ToModel: "claude-opus-4-8", Quality: 0.95},
	}}, spend.NewEngine(spend.DefaultTable()))

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-haiku-4-5",
		Body:        bodyWithModel(t, "claude-haiku-4-5", nil),
		InputTokens: 400_000, OutputTokens: 100_000,
	})
	if len(recs) != 1 {
		t.Fatalf("recs = %d", len(recs))
	}
	if got := recs[0].EstimatedSavingsTokens; got != 500_000 {
		t.Errorf("EstimatedSavingsTokens = %d, want 500000", got)
	}
	if got := recs[0].EstimatedSavingsUSD; got != 0 {
		t.Errorf("EstimatedSavingsUSD = %f, want 0 (never negative)", got)
	}
}

// On a flat-rate subscription the spend engine prices every request at
// zero. The router must inherit the request's CostSource instead of
// silently pricing plan-covered traffic at list rates.
func TestPlanIncludedRouteReportsNoPhantomUSD(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-haiku-4-5", Quality: 0.8},
	}}, spend.NewEngine(spend.DefaultTable()))

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body:        bodyWithModel(t, "claude-opus-4-8", nil),
		InputTokens: 1_000_000, OutputTokens: 200_000,
		CostSource: eventschema.CostSourcePlanIncluded,
	})
	if len(recs) != 1 {
		t.Fatalf("recs = %d", len(recs))
	}
	if got := recs[0].EstimatedSavingsUSD; got != 0 {
		t.Errorf("EstimatedSavingsUSD = %f, want 0 — plan-covered traffic has no dollar saving", got)
	}
	if got := recs[0].EstimatedSavingsTokens; got != 1_200_000 {
		t.Errorf("EstimatedSavingsTokens = %d, want 1200000 — tokens are the real currency here", got)
	}
}

// Metered traffic keeps the dollar path intact.
func TestMeteredRouteStillPricesInUSD(t *testing.T) {
	r := New(Config{Rules: []Rule{
		{Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-haiku-4-5", Quality: 0.8},
	}}, spend.NewEngine(spend.DefaultTable()))

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body:        bodyWithModel(t, "claude-opus-4-8", nil),
		InputTokens: 1_000_000, OutputTokens: 200_000,
		CostSource: eventschema.CostSourceMetered,
	})
	if len(recs) != 1 {
		t.Fatalf("recs = %d", len(recs))
	}
	if recs[0].EstimatedSavingsUSD <= 0 {
		t.Errorf("metered route should still price in USD, got %f", recs[0].EstimatedSavingsUSD)
	}
}

// --- task-aware routing --------------------------------------------------

func chatBody(t *testing.T, model, instruction string, toolPairs int) []byte {
	t.Helper()
	msgs := make([]any, 0, 1+2*toolPairs)
	msgs = append(msgs, map[string]any{"role": "user", "content": instruction})
	for range toolPairs {
		msgs = append(msgs,
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "name": "Bash"}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "content": "ok"}}},
		)
	}
	b, err := json.Marshal(map[string]any{"model": model, "messages": msgs})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// A rule scoped to mechanical work fires on a terse directive over heavy
// tool traffic — the case where a cheaper model costs the operator nothing.
func TestClassScopedRuleAppliesToMechanicalTurn(t *testing.T) {
	r := New(Config{Rules: []Rule{{
		Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
		ToModel: "claude-haiku-4-5", Quality: 0.9, WhenClass: string(taskclass.Mechanical),
	}}}, nil)

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: chatBody(t, "claude-opus-4-8", "continue", 3),
	})
	if len(recs) != 1 {
		t.Fatalf("recs = %d, want 1 for a mechanical turn", len(recs))
	}
	if !strings.Contains(recs[0].Reason, "mechanical") {
		t.Errorf("reason should name the class: %q", recs[0].Reason)
	}
}

// The same rule must not fire when the operator is doing reasoning work.
// Silently downgrading the model there is the trade they did not ask for.
func TestClassScopedRuleSkipsReasoningTurn(t *testing.T) {
	long := strings.Repeat("weigh the tradeoffs and justify the boundary ", 30)
	r := New(Config{Rules: []Rule{{
		Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
		ToModel: "claude-haiku-4-5", Quality: 0.9, WhenClass: string(taskclass.Mechanical),
	}}}, nil)

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: chatBody(t, "claude-opus-4-8", long, 1),
	})
	if len(recs) != 0 {
		t.Errorf("recs = %d, want 0 — a reasoning turn must not be routed down: %+v", len(recs), recs)
	}
}

// An ambiguous turn is not routed either: the classifier abstains and the
// rule declines rather than guessing.
func TestClassScopedRuleSkipsUnknownTurn(t *testing.T) {
	r := New(Config{Rules: []Rule{{
		Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
		ToModel: "claude-haiku-4-5", Quality: 0.9, WhenClass: string(taskclass.Mechanical),
	}}}, nil)

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: chatBody(t, "claude-opus-4-8", "add a test for the retry path and run it", 1),
	})
	if len(recs) != 0 {
		t.Errorf("recs = %d, want 0 for an unclassifiable turn", len(recs))
	}
}

// An unscoped rule keeps its existing unconditional behaviour, so no
// existing configuration changes meaning.
func TestUnscopedRuleUnaffectedByClass(t *testing.T) {
	long := strings.Repeat("weigh the tradeoffs and justify the boundary ", 30)
	r := New(Config{Rules: []Rule{{
		Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
		ToModel: "claude-haiku-4-5", Quality: 0.9,
	}}}, nil)

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: chatBody(t, "claude-opus-4-8", long, 1),
	})
	if len(recs) != 1 {
		t.Errorf("recs = %d, want 1 — an unscoped rule still applies unconditionally", len(recs))
	}
}

// --- preferred model: ceiling + fallback ---------------------------------

// The operator's preferred model is a ceiling. A rule that would route
// them UP to something pricier is refused rather than applied silently —
// that is a bill increase they did not agree to.
func TestUpgradeAbovePreferredIsRefused(t *testing.T) {
	var proposed []Proposal
	r := New(Config{
		Rules: []Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-fable-5", Quality: 0.95,
		}},
		PreferredModel: func(eventschema.Provider) string { return "claude-opus-4-8" },
		OnProposal:     func(p Proposal) { proposed = append(proposed, p) },
	}, spend.NewEngine(spend.DefaultTable()))

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body:        bodyWithModel(t, "claude-opus-4-8", nil),
		InputTokens: 1_000_000,
	})
	for _, rec := range recs {
		if rec.ApplyBody != nil {
			t.Errorf("an upgrade above the preferred model must not be applied")
		}
	}
	if len(proposed) != 1 {
		t.Fatalf("proposals = %d, want 1", len(proposed))
	}
	if proposed[0].ProposedModel != "claude-fable-5" {
		t.Errorf("ProposedModel = %q", proposed[0].ProposedModel)
	}
	if proposed[0].PreferredModel != "claude-opus-4-8" {
		t.Errorf("PreferredModel = %q — the fallback the operator can accept", proposed[0].PreferredModel)
	}
}

// Routing DOWN stays automatic. The ceiling exists to stop surprise
// upgrades, not to gate the savings the operator asked for.
func TestDowngradeBelowPreferredStillApplies(t *testing.T) {
	var proposed []Proposal
	r := New(Config{
		Rules: []Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-haiku-4-5", Quality: 0.9,
		}},
		PreferredModel: func(eventschema.Provider) string { return "claude-opus-4-8" },
		OnProposal:     func(p Proposal) { proposed = append(proposed, p) },
	}, spend.NewEngine(spend.DefaultTable()))

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: bodyWithModel(t, "claude-opus-4-8", nil), InputTokens: 1_000_000,
	})
	if len(recs) != 1 || recs[0].ApplyBody == nil {
		t.Fatalf("a cheaper route should still apply: %+v", recs)
	}
	if len(proposed) != 0 {
		t.Errorf("a downgrade needs no approval, got %+v", proposed)
	}
}

// Once the operator approves an upgrade, it applies without asking again.
func TestApprovedUpgradeApplies(t *testing.T) {
	r := New(Config{
		Rules: []Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-fable-5", Quality: 0.95,
		}},
		PreferredModel: func(eventschema.Provider) string { return "claude-opus-4-8" },
		UpgradeDecision: func(_ eventschema.Provider, from, to string) Decision {
			if from == "claude-opus-4-8" && to == "claude-fable-5" {
				return DecisionApproved
			}
			return DecisionPending
		},
	}, spend.NewEngine(spend.DefaultTable()))

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: bodyWithModel(t, "claude-opus-4-8", nil), InputTokens: 1_000_000,
	})
	if len(recs) != 1 || recs[0].ApplyBody == nil {
		t.Fatalf("an approved upgrade should apply: %+v", recs)
	}
}

// A denied upgrade stays refused and stops re-proposing.
func TestDeniedUpgradeStaysRefusedWithoutReproposing(t *testing.T) {
	var proposed []Proposal
	r := New(Config{
		Rules: []Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-fable-5", Quality: 0.95,
		}},
		PreferredModel:  func(eventschema.Provider) string { return "claude-opus-4-8" },
		UpgradeDecision: func(eventschema.Provider, string, string) Decision { return DecisionDenied },
		OnProposal:      func(p Proposal) { proposed = append(proposed, p) },
	}, spend.NewEngine(spend.DefaultTable()))

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: bodyWithModel(t, "claude-opus-4-8", nil), InputTokens: 1_000_000,
	})
	for _, rec := range recs {
		if rec.ApplyBody != nil {
			t.Errorf("a denied upgrade must not apply")
		}
	}
	if len(proposed) != 0 {
		t.Errorf("a decided upgrade must not be re-proposed, got %+v", proposed)
	}
}

// When either side has no rate card the comparison cannot be made. That
// is exactly when NOT to act: an unverifiable change to the operator's
// model is treated as an upgrade and referred to them.
func TestUnpricedTargetIsTreatedAsAnUpgrade(t *testing.T) {
	var proposed []Proposal
	r := New(Config{
		Rules: []Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-brand-new-unpriced", Quality: 0.95,
		}},
		PreferredModel: func(eventschema.Provider) string { return "claude-opus-4-8" },
		OnProposal:     func(p Proposal) { proposed = append(proposed, p) },
	}, spend.NewEngine(spend.DefaultTable()))

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: bodyWithModel(t, "claude-opus-4-8", nil), InputTokens: 1_000_000,
	})
	for _, rec := range recs {
		if rec.ApplyBody != nil {
			t.Errorf("an unpriced target must not be applied unreviewed")
		}
	}
	if len(proposed) != 1 {
		t.Errorf("proposals = %d, want 1 for an unverifiable route", len(proposed))
	}
}

// With no preferred model configured nothing changes.
func TestNoPreferredModelKeepsExistingBehaviour(t *testing.T) {
	r := New(Config{Rules: []Rule{{
		Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
		ToModel: "claude-fable-5", Quality: 0.95,
	}}}, spend.NewEngine(spend.DefaultTable()))

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: bodyWithModel(t, "claude-opus-4-8", nil), InputTokens: 1_000_000,
	})
	if len(recs) != 1 || recs[0].ApplyBody == nil {
		t.Fatalf("without a ceiling the rule applies as before: %+v", recs)
	}
}

// --- window-pressure routing ---------------------------------------------

// The point of the whole feature: on a flat-rate plan the scarce resource
// is the rate-limit window, not money. A rule scoped to pressure should sit
// idle while there is headroom, so the operator keeps their best model for
// as long as they can afford to.
func TestPressureRuleIdleWhenWindowIsRoomy(t *testing.T) {
	r := New(Config{
		Rules: []Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-haiku-4-5", Quality: 0.9, WhenWindowPctAbove: 70,
		}},
		WindowPressure: func(eventschema.Provider) (float64, bool) { return 12.5, true },
	}, nil)

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: bodyWithModel(t, "claude-opus-4-8", nil),
	})
	if len(recs) != 0 {
		t.Errorf("recs = %d, want 0 at 12.5%% of the window: %+v", len(recs), recs)
	}
}

// Once the window is genuinely tight, the same rule fires and says why.
func TestPressureRuleFiresWhenWindowIsTight(t *testing.T) {
	r := New(Config{
		Rules: []Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-haiku-4-5", Quality: 0.9, WhenWindowPctAbove: 70,
		}},
		WindowPressure: func(eventschema.Provider) (float64, bool) { return 82, true },
	}, nil)

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: bodyWithModel(t, "claude-opus-4-8", nil),
	})
	if len(recs) != 1 || recs[0].ApplyBody == nil {
		t.Fatalf("a tight window should trigger the route: %+v", recs)
	}
	if !strings.Contains(recs[0].Reason, "window") {
		t.Errorf("reason should explain the pressure: %q", recs[0].Reason)
	}
}

// No window signal means no basis to act on. Routing down because the meter
// is broken would degrade quality for a reason that does not exist — the
// meter read 0/200 for months on this very setup.
func TestPressureRuleIdleWhenWindowUnknown(t *testing.T) {
	r := New(Config{
		Rules: []Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-haiku-4-5", Quality: 0.9, WhenWindowPctAbove: 70,
		}},
		WindowPressure: func(eventschema.Provider) (float64, bool) { return 0, false },
	}, nil)

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: bodyWithModel(t, "claude-opus-4-8", nil),
	})
	if len(recs) != 0 {
		t.Errorf("recs = %d, want 0 when the window is unmeasured: %+v", len(recs), recs)
	}
}

// Same, when nothing supplies a window reading at all.
func TestPressureRuleIdleWithoutAProbe(t *testing.T) {
	r := New(Config{Rules: []Rule{{
		Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
		ToModel: "claude-haiku-4-5", Quality: 0.9, WhenWindowPctAbove: 70,
	}}}, nil)

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: bodyWithModel(t, "claude-opus-4-8", nil),
	})
	if len(recs) != 0 {
		t.Errorf("recs = %d, want 0 with no window probe wired", len(recs))
	}
}

// Pressure composes with the task class: preserve headroom by dropping
// mechanical turns, but never silently downgrade reasoning work, however
// tight the window gets.
func TestPressureAndClassCompose(t *testing.T) {
	rules := []Rule{{
		Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
		ToModel: "claude-haiku-4-5", Quality: 0.9,
		WhenWindowPctAbove: 70, WhenClass: string(taskclass.Mechanical),
	}}
	tight := Config{
		Rules:          rules,
		WindowPressure: func(eventschema.Provider) (float64, bool) { return 95, true },
	}

	mechanical := New(tight, nil)
	recs, _ := mechanical.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: chatBody(t, "claude-opus-4-8", "continue", 3),
	})
	if len(recs) != 1 || recs[0].ApplyBody == nil {
		t.Errorf("mechanical turn under pressure should route: %+v", recs)
	}

	reasoning := New(tight, nil)
	long := strings.Repeat("weigh the tradeoffs and justify the boundary ", 30)
	recs, _ = reasoning.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: chatBody(t, "claude-opus-4-8", long, 1),
	})
	if len(recs) != 0 {
		t.Errorf("reasoning work must not be downgraded even at 95%%: %+v", recs)
	}
}

// An unscoped rule keeps applying regardless of window state.
func TestUnscopedRuleIgnoresWindow(t *testing.T) {
	r := New(Config{
		Rules: []Rule{{
			Provider: eventschema.ProviderAnthropic, FromModel: "claude-opus-4-8",
			ToModel: "claude-haiku-4-5", Quality: 0.9,
		}},
		WindowPressure: func(eventschema.Provider) (float64, bool) { return 1, true },
	}, nil)

	recs, _ := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Model: "claude-opus-4-8",
		Body: bodyWithModel(t, "claude-opus-4-8", nil),
	})
	if len(recs) != 1 {
		t.Errorf("unscoped rule should still apply: %+v", recs)
	}
}

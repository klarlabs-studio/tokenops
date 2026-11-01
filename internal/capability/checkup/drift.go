package checkup

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// DriftDays is how far back the catalog-drift finding reads: long enough
// to hold five weekly windows on each side of a plan switch.
const DriftDays = 90

// largeDrift is the gap at which the finding is a notice, not info.
const largeDrift = 0.5

// catalogDrift raises a finding for each drifted ratio ProviderDrifts
// finds.
func catalogDrift(drifts map[string][]plans.TierDrift, observedOff bool) []Finding {
	providers := make([]string, 0, len(drifts))
	for p := range drifts {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	var out []Finding
	for _, provider := range providers {
		for _, d := range drifts[provider] {
			if d.Drifted {
				out = append(out, driftFinding(d, observedOff))
			}
		}
	}
	return out
}

// ProviderDrifts compares the ratio between tiers the operator's own
// window readings imply with the catalog's, for each bound provider
// (plans.TierDrifts holds the rule): every comparison the evidence
// allows, drifted or not, by provider. It needs the event store; without
// one, or when it cannot be read, it is nil.
func ProviderDrifts(ctx context.Context, o Options) map[string][]plans.TierDrift {
	if o.Events == nil || o.Price == nil || len(o.Plans) == 0 {
		return nil
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	since := o.Now.AddDate(0, 0, -DriftDays)
	envs, err := o.Events.ReadEvents(ctx, eventschema.EventTypePrompt, since)
	if err != nil {
		return nil
	}
	out := make(map[string][]plans.TierDrift, len(o.Plans))
	for provider, current := range o.Plans {
		in := plans.DriftInput{
			Provider: provider, Since: since,
			Readings: plans.QuotaReadingsFrom(envs, provider, since, o.Now),
			Turns:    plans.DriftTurnsFrom(envs, provider, since, o.Now, o.Price),
			PlanAt:   func(t time.Time) string { return o.PlanHistory.At(provider, t, current) },
		}
		out[provider] = plans.TierDrifts(in)
	}
	return out
}

// driftFinding states one drifted ratio with what it rests on.
func driftFinding(d plans.TierDrift, observedOff bool) Finding {
	level := LevelInfo
	if math.Abs(d.Gap) >= largeDrift {
		level = LevelNotice
	}
	direction := "below"
	if d.Gap > 0 {
		direction = "above"
	}
	return Finding{
		Kind: "catalog-drift", Level: level,
		Title: fmt.Sprintf("Your %s windows put %s at %.1fx %s; the catalog says %gx",
			d.Window, d.Higher.Display, d.ImpliedRatio, d.Lower.Display, d.CatalogRatio),
		Evidence: fmt.Sprintf("%d %s windows on %s and %d on %s, each spent at least %.0f%% on %s by list value and filled "+
			"%.0f–99%%: one percent held %.1fx the list value on %s (quartiles allow %.1fx–%.1fx), %.0f%% %s the catalog's %gx. "+
			"Windows that mixed model families are left out, since a percent weighs models differently; "+
			"use on claude.ai or another machine fills the vendor's percent without being counted here",
			d.Lower.Windows, d.Window, d.Lower.Display, d.Higher.Windows, d.Higher.Display,
			plans.MinDriftFamilyShare*100, d.ModelFamily, plans.MinDriftPeakPct,
			d.ImpliedRatio, d.Higher.Display, d.Low, d.High, math.Abs(d.Gap)*100, direction, d.CatalogRatio),
		Fix: driftFix(observedOff),
	}
}

// driftFix says what to do about a drifted ratio, and what this machine
// does meanwhile.
func driftFix(observedOff bool) string {
	fix := "check the vendor's plan page; if the limit changed, open an issue on TokenOps: the catalog changes by pull request (ADR 0002)"
	if observedOff {
		return fix + ". This machine's plan advice uses the catalog (`tokenops plan observed on` would use your figure meanwhile)"
	}
	return fix + ". Meanwhile this machine's plan advice uses your observed figure, labelled as observed " +
		"(`tokenops plan observed`; `tokenops plan observed reset` goes back to the catalog)"
}

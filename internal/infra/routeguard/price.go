package routeguard

import (
	"fmt"
	"math"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// QuotaPrice is the price of quota on the Claude plan (plans.QuotaPrice,
// ADR 0017), as the hooks read it.
type QuotaPrice = plans.QuotaPrice

// How a price was worked out (plans.PriceBasisPace and the rest).
const (
	PriceBasisPace      = plans.PriceBasisPace
	PriceBasisShareLeft = plans.PriceBasisShareLeft
	PriceBasisUsedUp    = plans.PriceBasisUsedUp
)

// FormatQuotaPrice renders a price as the reasons do (plans.FormatQuotaPrice).
func FormatQuotaPrice(v float64) string { return plans.FormatQuotaPrice(v) }

// ReadQuotaPrice reads the price of quota on the Claude plan from what is
// already on the machine, as ReadPressure does: the status line's reading
// first (it carries resets, so pace), the daemon's figures second (share
// left only). Nil when no fresh reading answers, which routes by the work
// alone. A non-nil price may be 0: the plan has room.
func ReadQuotaPrice(limitsPath string, figures boundaryledger.Dir, now time.Time) *QuotaPrice {
	if r, ok, err := claudelimits.Read(limitsPath); err == nil && ok && now.Sub(r.ObservedAt) <= pressureLimitsFreshFor {
		return PriceFromLimits(r, now)
	}
	if fig, ok, err := figures.GetFigures(); err == nil && ok && now.Sub(fig.ObservedAt) <= pressureFiguresFreshFor {
		return PriceFromFigures(fig, now)
	}
	return nil
}

// PriceFromLimits prices the status line's 5h and weekly windows and
// returns the binding one; nil for a stale reading or one with no window.
func PriceFromLimits(r claudelimits.Reading, now time.Time) *QuotaPrice {
	if r.ObservedAt.IsZero() || now.Sub(r.ObservedAt) > pressureLimitsFreshFor {
		return nil
	}
	p := plans.BindingPrice("Claude", limitsWindows(r), now)
	if p == nil {
		return nil
	}
	p.ObservedAt, p.FreshUntil = r.ObservedAt.UTC(), r.ObservedAt.Add(pressureLimitsFreshFor).UTC()
	p.Source = "claude_code_statusline"
	return p
}

// PriceFromFigures prices the daemon's figures for the Claude plan. They
// carry no reset, so the price is by share left alone.
func PriceFromFigures(fig boundaryledger.Figures, now time.Time) *QuotaPrice {
	if fig.ObservedAt.IsZero() || now.Sub(fig.ObservedAt) > pressureFiguresFreshFor {
		return nil
	}
	p := plans.BindingPrice("Claude", figuresWindows(fig), now)
	if p == nil {
		return nil
	}
	p.ObservedAt, p.FreshUntil = fig.ObservedAt.UTC(), fig.ObservedAt.Add(pressureFiguresFreshFor).UTC()
	p.Source = "daemon_glance"
	return p
}

// qualityGain is the share of a piece of work's value expected to be lost
// running it one tier below the tier it fits (tierForKind), for the kinds
// a quota price can move (pressedTier). Deep work loses more a tier down
// than a routine edit does. These are the defaults ADR 0017 records; the
// randomized holdout (#824) and moveverdict are what test them.
var qualityGain = map[taskclass.Kind]float64{
	taskclass.KindEdit: 0.12,
	taskclass.KindDeep: 0.20,
}

// defaultExtraUse is the extra plan use assumed for the tier up when
// neither model's price is known: half again.
const defaultExtraUse = 0.5

// Escalation is the choice between the tier a piece of work fits (Upper)
// and the tier below it (Lower) on a plan with a price of quota. Each
// option is worth its expected quality minus λ times its plan use, with
// Upper's quality and use as the unit:
//
//	U(Upper) − U(Lower) = Gain − λ × ExtraUse
//
// so the work is escalated to Upper only when its quality gain exceeds its
// λ-weighted extra plan use.
type Escalation struct {
	Kind  taskclass.Kind `json:"kind"`
	Upper string         `json:"upper"`
	Lower string         `json:"lower"`
	// Gain is the quality Upper adds over Lower, as a share of the
	// work's value.
	Gain float64 `json:"gain"`
	// ExtraUse is the share of Upper's plan use that Lower saves:
	// 1 − Lower's price ÷ Upper's, at API-equivalent rates.
	ExtraUse float64 `json:"extra_use"`
	Lambda   float64 `json:"lambda"`
	// QuotaCost is Lambda × ExtraUse.
	QuotaCost float64 `json:"quota_cost"`
	Escalated bool    `json:"escalated"`
	// Window is the binding window the price comes from ("Claude
	// weekly").
	Window string `json:"window"`
}

// Why says why the work was or was not escalated.
func (e Escalation) Why() string {
	cost := fmt.Sprintf("quota cost %.2f (price %s × %.0f%% more plan use)",
		e.QuotaCost, plans.FormatQuotaPrice(e.Lambda), e.ExtraUse*100)
	if e.Escalated {
		return fmt.Sprintf("%s work escalated to %s: its %.2f quality gain over %s exceeds its %s", e.Kind, e.Upper, e.Gain, e.Lower, cost)
	}
	return fmt.Sprintf("%s work not escalated to %s: its %.2f quality gain over %s does not exceed its %s", e.Kind, e.Upper, e.Gain, e.Lower, cost)
}

// Evidence is the escalation and the price behind it, as a ledger records
// them.
func (e Escalation) Evidence(p *QuotaPrice) string {
	s := p.Evidence()
	if s != "" {
		s += "; "
	}
	return s + "escalation: " + e.Why()
}

// weighEscalation decides between upper, the model the work fits, and
// lower, the one a tier below it, at the price p.
func weighEscalation(cat *modeltier.Catalog, provider eventschema.Provider, kind taskclass.Kind, p *QuotaPrice, upper, lower string) Escalation {
	e := Escalation{Kind: kind, Upper: upper, Lower: lower, Gain: qualityGain[kind]}
	if p != nil {
		e.Lambda, e.Window = p.Lambda, p.Vendor+" "+p.Window
	}
	e.ExtraUse = extraUse(cat.Resolve(provider, upper), cat.Resolve(provider, lower))
	e.QuotaCost = e.Lambda * e.ExtraUse
	e.Escalated = e.Gain > e.QuotaCost
	return e
}

// extraUse is the share of upper's plan use lower saves. Plan windows
// meter use at about the vendor's API-equivalent rates, so the rate card's
// prices stand in for it.
func extraUse(upper, lower modeltier.Resolution) float64 {
	switch {
	case upper.Free:
		return 0
	case lower.Free:
		return 1
	case upper.CostPerMillion <= 0 || lower.CostPerMillion <= 0:
		return defaultExtraUse
	}
	return math.Max(0, 1-lower.CostPerMillion/upper.CostPerMillion)
}

// Escalations are, for each kind of work a quota price can move, the
// choice it makes now between the tier the work fits and the one below,
// on the models offered; for `tokenops explain price`. A kind whose two
// tiers the menu cannot fill with two different models is left out.
func Escalations(p *QuotaPrice, catalog *modeltier.Catalog, provider eventschema.Provider, candidates []string) []Escalation {
	if catalog == nil || len(candidates) == 0 {
		return nil
	}
	cat := catalog.WithCandidates(candidates)
	var out []Escalation
	for _, kind := range []taskclass.Kind{taskclass.KindEdit, taskclass.KindDeep} {
		fits, _ := tierForKind(kind)
		below, _ := pressedTier(kind)
		upper, okU := cat.Target(provider, fits)
		lower, okL := cat.Target(provider, below)
		if !okU || !okL || upper == lower {
			continue
		}
		out = append(out, weighEscalation(cat, provider, kind, p, upper, lower))
	}
	return out
}

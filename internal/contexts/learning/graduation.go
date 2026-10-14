package learning

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

// Graduation (#662) publishes, per capability, the evidence a step up the
// autonomy ladder needs: shadow → advise → act. It is a reading of the
// realized-savings and follow-through ledgers, never a decision: nothing
// here promotes anything. The operator does, with the command it names.

// Stage is where a capability stands on the ladder.
type Stage string

// The stages, least to most autonomous.
const (
	// StageShadow: the capability records what it would do and does
	// nothing.
	StageShadow Stage = "shadow"
	// StageAdvise: it tells the operator, who decides.
	StageAdvise Stage = "advise"
	// StageAct: it acts, on its own or after approval.
	StageAct Stage = "act"
)

// StageOf is the stage a coach power's effective rung puts a capability
// on: off records only, advise tells, ask and autonomous act.
func StageOf(rung string) Stage {
	switch rung {
	case "advise":
		return StageAdvise
	case "ask", "autonomous":
		return StageAct
	}
	return StageShadow
}

// The bar. ADR 0005 demotes a kind above about a 10% effective false-
// positive rate; the same 90% is the precision a promotion needs, over
// enough events that one good week is not the whole case.
const (
	GraduationMinEvents    = 50
	GraduationMinPrecision = 0.90
)

// GraduationEvidence is what the ledgers show for one capability.
type GraduationEvidence struct {
	// Shadow counts the resolved calls the capability made without
	// delivering them (held back or observed only); ShadowConfirmed those
	// whose outcome showed the call was right.
	Shadow          int `json:"shadow"`
	ShadowConfirmed int `json:"shadow_confirmed"`
	// Advised counts the delivered interventions with a known outcome;
	// Accepted those the operator acted on or let stand.
	Advised  int `json:"advised"`
	Accepted int `json:"accepted"`
	// Realized is the saving the evidence shows, in RealizedUnit ("usd"
	// or "tokens").
	Realized     measurement.Value `json:"realized"`
	RealizedUnit string            `json:"realized_unit,omitempty"`
	// Harmed reports a holdout comparison that found the capability made
	// outcomes worse.
	Harmed bool `json:"harmed,omitempty"`
}

// Criterion is one condition of a promotion, with the bar and where the
// evidence stands.
type Criterion struct {
	Name string `json:"name"`
	Want string `json:"want"`
	Have string `json:"have"`
	Met  bool   `json:"met"`
}

// Graduation is one capability's place on the ladder and what the next
// step needs.
type Graduation struct {
	Capability string `json:"capability"`
	Stage      Stage  `json:"stage"`
	// Next is the stage the criteria are for, empty when the capability
	// already acts.
	Next     Stage       `json:"next,omitempty"`
	Criteria []Criterion `json:"criteria,omitempty"`
	// Ready reports every criterion met. It is a suggestion to the
	// operator; nothing promotes on it.
	Ready bool `json:"ready"`
}

// Graduate reads the evidence against the criteria for the capability's
// next stage.
func Graduate(capability string, stage Stage, ev GraduationEvidence) Graduation {
	g := Graduation{Capability: capability, Stage: stage}
	switch {
	case stage == StageShadow:
		g.Next = StageAdvise
		g.Criteria = shadowCriteria(ev)
	case stage == StageAdvise && capability == CapabilityReadGuard:
		// The guard advises by watching: its evidence for enforcing is
		// still the re-reads it would have refused, and the tokens they
		// cost when they went ahead.
		g.Next = StageAct
		g.Criteria = append(shadowCriteria(ev), realizedCriterion(ev))
	case stage == StageAdvise:
		g.Next = StageAct
		g.Criteria = []Criterion{
			countCriterion("advice with an outcome", ev.Advised),
			precisionCriterion("acted on", ev.Accepted, ev.Advised),
			realizedCriterion(ev),
			{Name: "harm", Want: "none", Have: harmWords(ev.Harmed), Met: !ev.Harmed},
		}
	default:
		return g
	}
	g.Ready = true
	for _, c := range g.Criteria {
		g.Ready = g.Ready && c.Met
	}
	return g
}

func shadowCriteria(ev GraduationEvidence) []Criterion {
	return []Criterion{
		countCriterion("shadow calls", ev.Shadow),
		precisionCriterion("shadow precision", ev.ShadowConfirmed, ev.Shadow),
	}
}

func countCriterion(name string, n int) Criterion {
	return Criterion{Name: name, Want: fmt.Sprintf("at least %d", GraduationMinEvents),
		Have: fmt.Sprint(n), Met: n >= GraduationMinEvents}
}

func precisionCriterion(name string, hit, of int) Criterion {
	c := Criterion{Name: name, Want: fmt.Sprintf("at least %.0f%%", GraduationMinPrecision*100), Have: "none yet"}
	if of > 0 {
		share := float64(hit) / float64(of)
		c.Have = fmt.Sprintf("%.0f%% (%d of %d)", share*100, hit, of)
		c.Met = share >= GraduationMinPrecision
	}
	return c
}

func realizedCriterion(ev GraduationEvidence) Criterion {
	c := Criterion{Name: "realized saving", Want: "above zero"}
	amount, ok := ev.Realized.Amount()
	switch {
	case !ok:
		c.Have = "not established"
		if why := ev.Realized.Caveat(); why != "" {
			c.Have += " (" + why + ")"
		}
	case ev.RealizedUnit == "tokens":
		c.Have = "~" + compactCount(amount) + " tokens"
		c.Met = amount > 0
	default:
		c.Have = fmt.Sprintf("$%.2f", amount)
		c.Met = amount > 0
	}
	return c
}

// compactCount renders a count as 950, 220k or 1.4M.
func compactCount(n float64) string {
	switch {
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", n/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0fk", n/1e3)
	}
	return fmt.Sprintf("%.0f", n)
}

func harmWords(harmed bool) string {
	if harmed {
		return "the holdout found worse outcomes"
	}
	return "none found"
}

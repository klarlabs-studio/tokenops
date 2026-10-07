package budget

import (
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// ExceededKind is the domain event a budget raises when its window's
// spend reaches its limit.
const ExceededKind = "budget.exceeded"

// Exceeded is the budget.exceeded payload. The USD fields carry a spend or
// equivalent budget, the token fields a token budget; Basis says which.
type Exceeded struct {
	BudgetID    string    `json:"BudgetID"`
	Basis       string    `json:"Basis,omitempty"`
	SpentUSD    float64   `json:"SpentUSD,omitempty"`
	LimitUSD    float64   `json:"LimitUSD,omitempty"`
	SpentTokens int64     `json:"SpentTokens,omitempty"`
	LimitTokens int64     `json:"LimitTokens,omitempty"`
	At          time.Time `json:"At"`
}

// ExceededEvent is the budget.exceeded event for a, when a says the
// budget is exceeded: a threshold alert at or past its limit. A warning
// at 75% and a forecast breach are findings about a budget that is not
// exceeded yet, and raise no event.
func ExceededEvent(a Alert, at time.Time) (*eventschema.Envelope, bool) {
	if a.Kind != AlertThresholdReached || a.Limit.Threshold() <= 0 || a.Fraction < 1 {
		return nil, false
	}
	e := Exceeded{BudgetID: a.Limit.Name, Basis: a.Limit.Basis, At: at}
	if a.Limit.TokenBased() {
		e.SpentTokens, e.LimitTokens = int64(a.ActualUSD), a.Limit.LimitTokens
	} else {
		e.SpentUSD, e.LimitUSD = a.ActualUSD, a.Limit.LimitUSD
	}
	env, err := eventschema.NewDomainEnvelope(ExceededKind, e, at, "budget", eventschema.Association{})
	if err != nil {
		return nil, false
	}
	return env, true
}

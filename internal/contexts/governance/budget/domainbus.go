package budget

import (
	"time"

	"go.klarlabs.de/tokenops/internal/domainevents"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EventPublisher is the narrow canonical-envelope port budget evaluation
// uses to publish BudgetExceeded events.
type EventPublisher interface {
	Publish(*eventschema.Envelope)
}

var budgetEventBus EventPublisher

// SetEventBus installs the canonical event bus the budget evaluator
// publishes to. Called once from the daemon composition root.
func SetEventBus(b EventPublisher) { budgetEventBus = b }

func publishExceeded(a Alert) {
	if budgetEventBus == nil {
		return
	}
	// Only fire BudgetExceeded for the genuine threshold-reached or
	// forecast-projected-overage signals — informational alerts (a.Limit
	// of 0) are skipped.
	if a.Limit.LimitUSD <= 0 {
		return
	}
	domainevents.PublishCanonical(budgetEventBus, domainevents.BudgetExceeded{
		BudgetID: a.Limit.Name,
		SpentUSD: a.ActualUSD,
		LimitUSD: a.Limit.LimitUSD,
		At:       time.Now(),
	}, "budget")
}

package budget

import (
	"time"

	"github.com/felixgeelhaar/tokenops/internal/domainevents"
)

// DomainBusPublisher is the narrow port budget evaluation uses to
// publish BudgetExceeded events. *internal/domainevents.Bus satisfies
// it.
type DomainBusPublisher interface {
	Publish(ev domainevents.Event)
}

var budgetBus DomainBusPublisher

// SetDomainBus installs the in-process domain bus the budget evaluator
// publishes to. Called once from the daemon composition root.
func SetDomainBus(b DomainBusPublisher) { budgetBus = b }

func publishExceeded(a Alert) {
	if budgetBus == nil {
		return
	}
	// Only fire BudgetExceeded for the genuine threshold-reached or
	// forecast-projected-overage signals — informational alerts (a.Limit
	// of 0) are skipped.
	if a.Limit.LimitUSD <= 0 {
		return
	}
	budgetBus.Publish(domainevents.BudgetExceeded{
		BudgetID: a.Limit.Name,
		SpentUSD: a.ActualUSD,
		LimitUSD: a.Limit.LimitUSD,
		At:       time.Now(),
	})
}

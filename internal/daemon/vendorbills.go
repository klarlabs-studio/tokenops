package daemon

import (
	"context"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
)

// vendorBillsFor is how long one reading of the providers' own figures
// serves the budget runtimes: both ask every few minutes, and the plan
// headroom behind it reads the event store.
const vendorBillsFor = time.Minute

// vendorBills is each provider's own figure for the operator's spend this
// calendar month, read from the plan headroom, for the budgets that count
// a provider's bill (budgets.Billed).
type vendorBills struct {
	read func(ctx context.Context, now time.Time) map[string]float64

	mu    sync.Mutex
	at    time.Time
	bills map[string]float64
}

func newVendorBills(deps func() headroom.Deps) *vendorBills {
	return &vendorBills{read: func(ctx context.Context, now time.Time) map[string]float64 {
		res, err := headroom.Compute(ctx, deps(), now)
		if err != nil {
			return map[string]float64{}
		}
		return headroom.Bills(res.Reports)
	}}
}

// Bill is a budgets.Bill.
func (v *vendorBills) Bill(ctx context.Context, provider string, now time.Time) (float64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.bills == nil || now.Sub(v.at) >= vendorBillsFor || now.Before(v.at) {
		v.bills, v.at = v.read(ctx, now), now
	}
	usd, ok := v.bills[provider]
	return usd, ok
}

// Package planoverlay is this machine's overlay of plan limits observed
// from its own window readings: showing it, keeping it from the tier
// comparisons the catalog-drift finding reads, going back to the catalog
// and observing again. The CLI, the daemon and every surface that shows
// plan advice use it; the shipped catalog changes only by pull request
// (ADR 0002).
package planoverlay

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/checkup"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/observedlimits"
)

// Limit is one observed limit, with the catalog's figure beside it.
type Limit = plans.ObservedLimit

// View is the overlay as surfaces show it: whether it is off, and the
// limits in force.
type View struct {
	Off       bool      `json:"off"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	Limits    []Limit   `json:"limits"`
}

func view(o plans.ObservedLimits) View {
	ls := o.Current()
	if ls == nil {
		ls = []Limit{}
	}
	return View{Off: o.Off, UpdatedAt: o.UpdatedAt, Limits: ls}
}

// Show reads the overlay.
func Show() (View, error) {
	f, err := observedlimits.Default()
	if err != nil {
		return View{}, err
	}
	o, err := f.Load()
	if err != nil {
		return View{}, err
	}
	return view(o), nil
}

// Keep puts what the tier comparisons support into the overlay; nothing
// when they could not be read. An overlay that is off stays off.
func Keep(drifts map[string][]plans.TierDrift, now time.Time) (View, error) {
	f, err := observedlimits.Default()
	if err != nil {
		return View{}, err
	}
	if drifts == nil {
		o, err := f.Load()
		return view(o), err
	}
	o, err := f.Update(drifts, now)
	return view(o), err
}

// Reset goes back to the catalog and stops observing until TurnOn.
func Reset(now time.Time) (View, error) {
	f, err := observedlimits.Default()
	if err != nil {
		return View{}, err
	}
	o, err := f.Reset(now)
	return view(o), err
}

// TurnOn observes again, and keeps what the readings o names support now.
func TurnOn(ctx context.Context, o checkup.Options) (View, error) {
	f, err := observedlimits.Default()
	if err != nil {
		return View{}, err
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	ov, err := f.TurnOn(o.Now)
	if err != nil {
		return View{}, err
	}
	if drifts := checkup.ProviderDrifts(ctx, o); drifts != nil {
		if ov, err = f.Update(drifts, o.Now); err != nil {
			return View{}, err
		}
	}
	return view(ov), nil
}

// Off reports whether the operator turned the overlay off; an overlay that
// cannot be read is not off.
func Off() bool {
	v, err := Show()
	return err == nil && v.Off
}

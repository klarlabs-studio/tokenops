package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"go.klarlabs.de/vitra/platform"
)

// The icon shows one plan: Auto, the tightest across every plan, unless
// the operator pinned one from its tab in the panel. The tray menu's
// "Ring: tightest plan" goes back to Auto.

// actionRingAuto is the tray menu item that returns the ring to Auto.
const actionRingAuto = "ring.auto"

// errBadRing refuses a pin naming a provider neither the glance nor the
// registry knows.
var errBadRing = errors.New("not a plan the menu bar knows")

// ringInput is ring.pin's argument: the provider to pin, "" for Auto.
type ringInput struct {
	Provider string `json:"provider"`
}

// ring is the provider the icon shows; "" is Auto.
func (m *menubar) ring() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings.Ring
}

// setRing pins the icon to provider's plan, or returns it to Auto with "",
// and remembers it across launches. A provider is accepted when the last
// glance reported it or the registry names it; anything else is refused
// and changes nothing.
func (m *menubar) setRing(provider string) error {
	m.mu.Lock()
	if provider != "" && !m.knowsProvider(provider) {
		m.mu.Unlock()
		return errBadRing
	}
	m.settings.Ring = provider
	s := m.settings
	m.mu.Unlock()
	if m.prefs == "" {
		return nil
	}
	if err := saveSettings(m.prefs, s); err != nil {
		fmt.Fprintln(os.Stderr, "tokenops-menubar: ring setting:", err)
	}
	return nil
}

// knowsProvider reports whether provider is in the registry or the last
// glance. The caller holds m.mu.
func (m *menubar) knowsProvider(provider string) bool {
	if _, ok := providerNames[provider]; ok {
		return true
	}
	var g glanceView
	if json.Unmarshal(m.last.Glance, &g) != nil {
		return false
	}
	for _, r := range g.PlanHeadroom.Reports {
		if r.Provider == provider {
			return true
		}
	}
	return false
}

// ringItem is the tray menu's way back to Auto, checked while the ring
// follows the tightest plan.
func (m *menubar) ringItem() platform.MenuItem {
	return platform.MenuItem{ID: actionRingAuto, Label: "Ring: tightest plan", Checked: m.ring() == ""}
}

// pinRing is ring.pin: check, remember, and show the new ring at once.
func (m *menubar) pinRing(ctx context.Context, in ringInput) (view, error) {
	if err := m.setRing(in.Provider); err != nil {
		return view{}, err
	}
	return m.refresh(ctx), nil
}

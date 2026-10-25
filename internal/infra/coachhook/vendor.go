package coachhook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// The coach stands down where the vendor already speaks, as the router
// stand-down does for model choice (ADR 0009): a tip that only restates
// what the harness's own surface says is noise, and noise is what makes
// the tips that do add something go unread. Each stand-down is recorded
// with its reason, so a quiet coach can be told from a broken one.
const (
	// SuppressedVendorSpeaks: the vendor's own surface says what the tip
	// would, and the tip had nothing to add.
	SuppressedVendorSpeaks = "vendor_speaks"
	// SuppressedMostlyUnseen: a tip that restates the vendor and that
	// mostly went unseen in the last week, already said today; it waits
	// on the status line instead of interrupting again.
	SuppressedMostlyUnseen = "mostly_unseen"
)

// vendorSurface is a place where a vendor already tells the operator what
// one of the coach's tips says.
type vendorSurface struct {
	// Kind is the follow-through kind of the tip it overlaps.
	Kind string
	// Provider is the plan the surface belongs to ("anthropic" is
	// Claude Code). Codex, Cursor and opencode sessions never match it.
	Provider string
	// Surface names the vendor's surface.
	Surface string
	// Verified says the surface is checked to exist in the harness. Only
	// a verified surface stands a tip down; an unverified one is
	// recorded so it can be checked, and only lowers how often the tip
	// interrupts, where the ledger shows it mostly goes unseen.
	Verified bool
	// Source is where the surface was seen.
	Source string
}

// vendorSurfaces is the evidence behind every stand-down.
//
// The session's Claude Code version is not recorded by TokenOps (the hook
// reads usage and model from the transcript, not the client version), so
// no entry can be gated on one. The only gate is the provider: the hook
// that sees an anthropic plan window is Claude Code's.
var vendorSurfaces = []vendorSurface{
	{
		Kind: "quota_100", Provider: "anthropic", Surface: "Claude Code's own limit message", Verified: true,
		Source: "Claude Code answers a prompt at a used-up window with its own limit reached, resets-at message",
	},
	{
		Kind: burnKind, Provider: "anthropic", Surface: "Claude's weekly pace banner", Verified: false,
		Source: "seen in the Claude desktop app on 2026-10-24; not verified in Claude Code or its changelog",
	},
}

func surfaceFor(kind, provider string) (vendorSurface, bool) {
	for _, s := range vendorSurfaces {
		if s.Kind == kind && s.Provider == provider {
			return s, true
		}
	}
	return vendorSurface{}, false
}

// SurfaceDescription names the vendor surface a stood-down kind defers
// to, for the stats view; empty when the kind has none.
func SurfaceDescription(kind string) string {
	for _, s := range vendorSurfaces {
		if s.Kind == kind && s.Verified {
			return s.Surface
		}
	}
	return ""
}

// Alternative is another plan with room left: what a cross-plan finding
// is made of, and something no vendor's surface says about a rival's.
type Alternative struct {
	// Provider is the other plan's provider ("openai").
	Provider string
	// Window is its most constrained window.
	Window plans.QuotaWindow
}

// alternativeMinLeft is the share of its window another plan must have
// left to be worth moving work to.
const alternativeMinLeft = 25.0

// usableAlternatives keeps the plans, other than from, with room left.
func usableAlternatives(alts []Alternative, from string) []Alternative {
	var out []Alternative
	for _, a := range alts {
		if a.Provider != from && 100-a.Window.UsedPct >= alternativeMinLeft {
			out = append(out, a)
		}
	}
	return out
}

func alternativesNote(alts []Alternative) string {
	if len(alts) == 0 {
		return ""
	}
	msg := " Another plan has room:"
	for i, a := range alts {
		if i > 0 {
			msg += ","
		}
		msg += fmt.Sprintf(" %s (%s)", windowName(a.Provider, a.Window), shareLeft(a.Window.UsedPct))
	}
	return msg + " — move the work there."
}

// burnSaidFile remembers the day each provider's run-out warning last
// interrupted.
const burnSaidFile = "burn-said.json"

func readBurnSaid(dir string) map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(dir, burnSaidFile)); err == nil { //nolint:gosec // TokenOps' own state
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func burnSaidToday(dir, provider string, now time.Time) bool {
	return readBurnSaid(dir)[provider] == dayOf(now)
}

func markBurnSaid(dir, provider string, now time.Time) {
	m := readBurnSaid(dir)
	m[provider] = dayOf(now)
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	final := filepath.Join(dir, burnSaidFile)
	tmp := fmt.Sprintf("%s.%d.tmp", final, os.Getpid())
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, final)
	}
}

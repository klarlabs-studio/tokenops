package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/vitra"
	"go.klarlabs.de/vitra/domain"
)

// Pinned, the ring shows that plan even when another is tighter; the
// tooltip still lists every plan's windows.
func TestRingShowsThePinnedPlan(t *testing.T) {
	st := statusOf([]byte(glanceSample), "anthropic")
	if st.Title != "Claude 85% left" || st.Pct != 15 || !st.Known {
		t.Fatalf("status %+v", st)
	}
	for _, want := range []string{"Claude · 5h 94% left", "Codex · week 51% left", "Fireworks · $41.50 of $100.00"} {
		if !strings.Contains(st.Tooltip, want) {
			t.Errorf("tooltip lacks %q:\n%s", want, st.Tooltip)
		}
	}
	st = statusOf([]byte(glanceSample), "fireworks")
	if st.Title != "Fireworks 58% left" || st.Pct != 41.5 {
		t.Fatalf("spend pin %+v", st)
	}
}

// A pinned plan with no reading falls back to the tightest one and says
// so, rather than showing an unknown ring.
func TestRingFallsBackWhenThePinnedPlanHasNoReading(t *testing.T) {
	st := statusOf([]byte(glanceSample), "cursor")
	if st.Title != "Codex 51% left" || !st.Known {
		t.Fatalf("status %+v", st)
	}
	first, _, _ := strings.Cut(st.Tooltip, "\n")
	if first != "Pinned plan Cursor has no reading; showing the tightest plan." {
		t.Errorf("first tooltip line %q", first)
	}
}

// Auto is the tightest window across every plan, as before.
func TestRingAutoIsTheTightestPlan(t *testing.T) {
	if st := statusOf([]byte(glanceSample), ""); st.Title != "Codex 51% left" || strings.Contains(st.Tooltip, "Pinned") {
		t.Fatalf("status %+v", st)
	}
}

func TestRingSettingPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "TokenOps", "menubar.json")
	if loadSettings(path).Ring != "" {
		t.Error("the ring is pinned by default")
	}
	if err := saveSettings(path, settings{Ring: "openai"}); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(path).Ring; got != "openai" {
		t.Errorf("ring = %q", got)
	}
}

// setRing takes a provider the glance or the registry knows, or "" for
// Auto, and remembers it; anything else is refused and changes nothing.
func TestSetRing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "TokenOps", "menubar.json")
	m := &menubar{prefs: path}
	m.last.Glance = []byte(`{"plan_headroom":{"reports":[{"provider":"my-gateway","windows":[{"name":"day","used_pct":3}]}]}}`)
	for _, bad := range []string{"nope", "../etc", "Anthropic"} {
		if err := m.setRing(bad); !errors.Is(err, errBadRing) {
			t.Errorf("setRing(%q) = %v", bad, err)
		}
	}
	if loadSettings(path).Ring != "" {
		t.Fatal("a refused pin was saved")
	}
	for _, ok := range []string{"anthropic", "my-gateway", ""} {
		if err := m.setRing(ok); err != nil {
			t.Fatalf("setRing(%q) = %v", ok, err)
		}
		if got := loadSettings(path).Ring; got != ok || m.ring() != ok {
			t.Errorf("after setRing(%q): saved %q, held %q", ok, got, m.ring())
		}
	}
}

// The tray menu says when the ring follows the tightest plan.
func TestRingMenuItem(t *testing.T) {
	m := &menubar{}
	if it := m.ringItem(); it.ID != actionRingAuto || !it.Checked || it.Label != "Ring: tightest plan" {
		t.Errorf("auto item %+v", it)
	}
	m.settings.Ring = "openai"
	if it := m.ringItem(); it.Checked {
		t.Errorf("pinned item %+v", it)
	}
}

// The panel may call ring.pin; its input is checked before anything runs.
func TestRingPinIsWiredToThePanel(t *testing.T) {
	rt, err := vitra.New(vitra.Config{AppID: appID})
	if err != nil {
		t.Fatal(err)
	}
	m := &menubar{rt: rt, prefs: filepath.Join(t.TempDir(), "menubar.json")}
	if err := m.register(); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.OpenWindow(context.Background(), panelWindow, domain.OriginPackagedLocal); err != nil {
		t.Fatal(err)
	}
	panel, _ := domain.NewCaller(panelWindow, domain.OriginPackagedLocal)
	_, err = rt.Invoke(context.Background(), domain.InvocationRequest{Caller: panel, Command: "ring.pin", Input: map[string]any{"provider": "nope"}})
	var denied *domain.ErrDenied
	if errors.As(err, &denied) || !errors.Is(err, errBadRing) {
		t.Errorf("ring.pin = %v, want the handler's input check", err)
	}
}

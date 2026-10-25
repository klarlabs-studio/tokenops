package headroom

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// Under pressure the glance tells the agent, in one line, to pass a
// cheaper model on subagent calls for work that fits, and why; with no
// pressure it says nothing.
func TestRoutingAdviceOnlyUnderPressure(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	pressed := plans.SessionBudget{Provider: "anthropic", Windows: []plans.VendorWindow{
		{Name: "5h", UsedPct: 10, Duration: 5 * time.Hour, ResetsAt: now.Add(4 * time.Hour)},
		{Name: "week", UsedPct: 46, Duration: week, ResetsAt: now.Add(5*24*time.Hour + 18*time.Hour)},
	}}
	calm := plans.SessionBudget{Provider: "openai", Windows: []plans.VendorWindow{
		{Name: "week", UsedPct: 46, Duration: week, ResetsAt: now.Add(3*24*time.Hour + 18*time.Hour)},
	}}
	got := RoutingAdvice([]plans.SessionBudget{pressed, calm}, now)
	if len(got) != 1 || got[0].Provider != "anthropic" || got[0].Pressure == nil {
		t.Fatalf("advice %+v", got)
	}
	a := got[0].Advice
	if !strings.HasPrefix(a, "Claude weekly runs out ~") || !strings.Contains(a, "`model`") || strings.Contains(a, "\n") {
		t.Errorf("advice = %q", a)
	}
	if got := RoutingAdvice([]plans.SessionBudget{calm}, now); len(got) != 0 {
		t.Errorf("calm plan advised: %+v", got)
	}
	if p := (Glance{Routing: RoutingAdvice([]plans.SessionBudget{pressed}, now)}).Payload(); len(p.Routing) != 1 {
		t.Errorf("payload routing %+v", p.Routing)
	}
}

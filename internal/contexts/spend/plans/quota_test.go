package plans

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Shapes copied from a live claude.ai meter reading and a live Codex turn.
var (
	claudeMeter = map[string]string{
		"five_hour_kind": "session", "five_hour_reset_at": "2026-09-28T01:59:59.825543+00:00", "five_hour_used_pct": "12.00",
		"seven_day_kind": "weekly_all", "seven_day_reset_at": "2026-10-02T22:59:59.825567+00:00", "seven_day_used_pct": "22.00",
		"weekly_scoped_fable_kind": "weekly_scoped", "weekly_scoped_fable_model_scope": "Fable",
		"weekly_scoped_fable_reset_at": "2026-10-02T22:59:59.825779+00:00", "weekly_scoped_fable_used_pct": "64.00",
		"nimbus_quill_used_pct": "0.00", "org_id": "org",
	}
	codexTurn = map[string]string{
		"plan_type": "prolite", "primary_used_pct": "100.00", "primary_window_min": "10080", "primary_resets_at": "1791047395",
		"secondary_used_pct": "0.00", "secondary_window_min": "0", "secondary_resets_at": "0",
	}
)

func TestQuotaWindowsFromClaudeMeter(t *testing.T) {
	ws := QuotaWindowsFromAttributes(eventschema.ProviderAnthropic, claudeMeter)
	got := map[string]QuotaWindow{}
	for _, w := range ws {
		got[w.Label] = w
	}
	if len(ws) != 3 {
		t.Fatalf("windows = %+v, want 5-hour, weekly, weekly Fable (a meter key with no reset is not a window)", ws)
	}
	if w := got["5-hour"]; w.UsedPct != 12 || w.Duration != 5*time.Hour {
		t.Errorf("5-hour = %+v", w)
	}
	if w := got["weekly"]; w.UsedPct != 22 || w.Duration != 7*24*time.Hour {
		t.Errorf("weekly = %+v", w)
	}
	if w := got["weekly Fable"]; w.UsedPct != 64 || w.ResetsAt.IsZero() {
		t.Errorf("weekly Fable = %+v", w)
	}
	most, ok := MostConstrained(ws)
	if !ok || most.Label != "weekly Fable" {
		t.Errorf("most constrained = %+v, want weekly Fable", most)
	}
}

func TestQuotaWindowsFromCodexTurn(t *testing.T) {
	ws := QuotaWindowsFromAttributes(eventschema.ProviderOpenAI, codexTurn)
	if len(ws) != 1 {
		t.Fatalf("windows = %+v, want only the primary (secondary reports no window)", ws)
	}
	w := ws[0]
	if w.Label != "weekly" || w.UsedPct != 100 || w.Duration != 10080*time.Minute || w.ResetsAt.Unix() != 1791047395 {
		t.Errorf("codex primary = %+v", w)
	}
}

func TestQuotaWindowsIgnoreUnknownProvider(t *testing.T) {
	if ws := QuotaWindowsFromAttributes(eventschema.ProviderCursor, claudeMeter); len(ws) != 0 {
		t.Errorf("cursor windows = %+v", ws)
	}
}

// Pace is what turns a percentage into advice: 60% used two days into a
// week runs out long before the reset; 60% used six days in does not.
func TestQuotaPace(t *testing.T) {
	reset := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	w := QuotaWindow{Label: "weekly", UsedPct: 60, ResetsAt: reset, Duration: 7 * 24 * time.Hour}

	early := reset.Add(-5 * 24 * time.Hour) // two days in
	at, runsOut := w.ProjectedExhaustion(early)
	if !runsOut {
		t.Fatal("60% two days into a week should run out before the reset")
	}
	if want := early.Add(time.Duration(float64(2*24*time.Hour) * 40 / 60)); at.Sub(want).Abs() > time.Minute {
		t.Errorf("projected exhaustion %v, want %v", at, want)
	}
	if _, runsOut := w.ProjectedExhaustion(reset.Add(-24 * time.Hour)); runsOut {
		t.Error("60% six days in should last to the reset")
	}
	if _, runsOut := (QuotaWindow{UsedPct: 0, ResetsAt: reset, Duration: time.Hour}).ProjectedExhaustion(reset.Add(-30 * time.Minute)); runsOut {
		t.Error("nothing used cannot run out")
	}
}

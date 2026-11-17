package findings

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/config"
)

func effortSnapshot(client, model string) *SessionsSnapshot {
	rows := []sessions.EffortRow{
		{Client: client, Model: model, Effort: "medium", Instructions: 40, Enough: true, FirstTryPct: 70, RejectedPct: 5, InterruptedPct: 4, USDPerInstruction: 0.4},
		{Client: client, Model: model, Effort: "xhigh", Instructions: 120, Enough: true, FirstTryPct: 71, RejectedPct: 5, InterruptedPct: 4, USDPerInstruction: 1.2},
	}
	s := &SessionsSnapshot{}
	s.DX.Metrics.ByEffort = rows
	return s
}

func coachWithModels(rung string) *coach.Report {
	return &coach.Report{Powers: []coach.Power{{Name: config.PowerModels, Effective: rung}}}
}

func TestEffortFindings(t *testing.T) {
	tests := []struct {
		name   string
		snap   *SessionsSnapshot
		coach  *coach.Report
		want   int
		change string
	}{
		{name: "claude code names the per-model setting", snap: effortSnapshot("claude-code", "claude-opus-5-5"),
			coach: coachWithModels(config.AutonomyAdvise), want: 1, change: `"effortLevel": "medium"`},
		{name: "codex names its config key", snap: effortSnapshot("codex", "gpt-5.5"),
			coach: coachWithModels(config.AutonomyAutonomous), want: 1, change: "model_reasoning_effort"},
		{name: "opencode is advised without a setting", snap: effortSnapshot("opencode", "claude-opus-5-5"),
			coach: coachWithModels(config.AutonomyAdvise), want: 1},
		{name: "the models power off says nothing", snap: effortSnapshot("claude-code", "claude-opus-5-5"),
			coach: coachWithModels(config.AutonomyOff)},
		{name: "no session analysis says nothing", coach: coachWithModels(config.AutonomyAdvise)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effortFindings(tt.snap, tt.coach)
			if len(got) != tt.want {
				t.Fatalf("findings = %+v, want %d", got, tt.want)
			}
			if tt.want == 0 {
				return
			}
			f := got[0]
			if f.Kind != KindCoach || f.Level != LevelInfo || !strings.HasPrefix(f.Key, "coach:effort:") {
				t.Errorf("finding = %+v", f)
			}
			for _, w := range []string{"xhigh", "medium", "67% less", "harder work", "estimate"} {
				if !strings.Contains(f.Title+f.Evidence+f.Action, w) {
					t.Errorf("finding does not say %q: %+v", w, f)
				}
			}
			if tt.change != "" && !strings.Contains(f.Action, tt.change) {
				t.Errorf("action %q does not name %q", f.Action, tt.change)
			}
			if tt.change == "" && strings.Contains(f.Action, "settings.json") {
				t.Errorf("action names a setting for a client without one: %q", f.Action)
			}
			if !strings.Contains(f.Action, "does not change") {
				t.Errorf("action does not say TokenOps leaves the setting: %q", f.Action)
			}
		})
	}
}

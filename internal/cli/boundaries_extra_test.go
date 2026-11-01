package cli

import (
	"strings"
	"testing"
	"time"
)

// An extra allowance needs a person at a terminal; granted, it lets the
// agent past the boundary it names, shows in status, and --end puts the
// boundary back.
func TestBoundariesExtra(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "0")
	boundaryHome(t, spendBoundary+"    stage: enforce\n", 25)
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("before the extra: %+v", d)
	}
	if _, err := runCLI(t, nil, "boundaries", "extra", "30m", "--rule", "session-spend"); err == nil ||
		!strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("without a terminal: %v", err)
	}
	atTerminal(t)
	if _, err := runCLI(t, nil, "boundaries", "extra", "30m"); err == nil || !strings.Contains(err.Error(), "plan headroom") {
		t.Fatalf("no headroom rule to lift by default: %v", err)
	}
	out, err := runCLI(t, nil, "boundaries", "extra", "30m", "--rule", "session-spend", "--why", "release")
	if err != nil || !strings.Contains(out, "extra allowance bnd-") || !strings.Contains(out, "session-spend") {
		t.Fatalf("grant = %q, %v", out, err)
	}
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("under the extra: %+v", d)
	}
	out, err = runCLI(t, nil, "boundaries", "status")
	if err != nil || !strings.Contains(out, "extra allowance until") || !strings.Contains(out, "(release)") {
		t.Fatalf("status = %q, %v", out, err)
	}
	out, err = runCLI(t, nil, "boundaries", "extra", "--end")
	if err != nil || !strings.Contains(out, "ended bnd-") {
		t.Fatalf("end = %q, %v", out, err)
	}
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("after --end: %+v", d)
	}
}

func TestParseUntil(t *testing.T) {
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.Local)
	tests := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{in: "18:00", want: time.Date(2026, 11, 1, 18, 0, 0, 0, time.Local)},
		{in: "09:30", want: time.Date(2026, 11, 2, 9, 30, 0, 0, time.Local)},
		{in: "15:00", want: time.Date(2026, 11, 2, 15, 0, 0, 0, time.Local)},
		{in: "2026-11-01T20:00:00Z", want: time.Date(2026, 11, 1, 20, 0, 0, 0, time.UTC)},
		{in: "six", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseUntil(tc.in, now)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %v, want an error", got)
				}
				return
			}
			if err != nil || !got.Equal(tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

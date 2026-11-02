package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/presence/presencetest"
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

// Under presence the grant is signed through the helper and lets the agent
// past; an edited extras.json lifts nothing and status says so; nobody at
// the screen refuses the grant; --end needs no presence.
func TestBoundariesExtraUnderPresence(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "0")
	home := boundaryHome(t, spendBoundary+"    stage: enforce\nboundary_approval: presence\n", 25)
	f := presencetest.Use(t)
	atTerminal(t)
	if _, err := runCLI(t, nil, "boundaries", "extra", "30m", "--rule", "session-spend"); err == nil ||
		!strings.Contains(err.Error(), "no presence key is enrolled") {
		t.Fatalf("without a key: %v", err)
	}
	if _, err := runCLI(t, nil, "boundaries", "presence", "enroll"); err != nil {
		t.Fatal(err)
	}
	f.Fail(presencetest.ExitNoGUI)
	if _, err := runCLI(t, nil, "boundaries", "extra", "30m", "--rule", "session-spend"); err == nil ||
		!strings.Contains(err.Error(), "nothing was granted") {
		t.Fatalf("nobody at the screen: %v", err)
	}
	f.Fail(0)
	out, err := runCLI(t, nil, "boundaries", "extra", "30m", "--rule", "session-spend", "--why", "release")
	if err != nil || !strings.Contains(out, "signed with presence key") {
		t.Fatalf("grant = %q, %v", out, err)
	}
	calls := f.Calls()
	if last := calls[len(calls)-1]; !strings.Contains(last, "extra allowance lifting session-spend") || !strings.Contains(last, "release") {
		t.Fatalf("dialog %q", last)
	}
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("under the signed extra: %+v", d)
	}
	if out, err := runCLI(t, nil, "boundaries", "status"); err != nil || !strings.Contains(out, "presence-signed with key") {
		t.Fatalf("status = %q, %v", out, err)
	}

	// Stretching the grant in extras.json breaks its signature.
	path := filepath.Join(home, ".tokenops", "boundaries", "extras.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var extras []map[string]any
	if err := json.Unmarshal(b, &extras); err != nil || len(extras) != 1 {
		t.Fatalf("extras %s %v", b, err)
	}
	extras[0]["until"] = time.Now().Add(20 * time.Hour).UTC().Format(time.RFC3339)
	b, _ = json.Marshal(extras)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("an edited extra let the agent on: %+v", d)
	}
	if out, err := runCLI(t, nil, "boundaries", "status"); err != nil || !strings.Contains(out, "NOT honoured") {
		t.Fatalf("status = %q, %v", out, err)
	}

	f.Fail(presencetest.ExitNoGUI)
	before := len(f.Calls())
	if out, err := runCLI(t, nil, "boundaries", "extra", "--end"); err != nil || !strings.Contains(out, "ended bnd-") {
		t.Fatalf("end = %q, %v", out, err)
	}
	if len(f.Calls()) != before {
		t.Fatal("--end asked the helper for a signature")
	}
}

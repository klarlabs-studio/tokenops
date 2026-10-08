package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var kiroNow = time.Date(2026, 5, 20, 12, 0, 0, 0, time.Local)

func TestKiroReadsTheUsageReport(t *testing.T) {
	bin, log := fakeCLI(t, map[string]fakeAnswer{
		"whoami": ok("Logged in with Google\nEmail: user@example.com\n"),
		"chat":   ok(readFixture(t, "kiro.txt")),
	})
	got, err := Kiro{Bin: bin, Now: func() time.Time { return kiroNow }}.Read(context.Background(), "")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "month" || w.UsedPct != 0 ||
		!w.ResetsAt.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)) {
		t.Errorf("monthly %+v", w)
	}
	if w := got.Windows[1]; w.Name != "bonus credits" || !approx(w.UsedPct, 2.2765) || !w.ResetsAt.Equal(kiroNow.Add(19*24*time.Hour)) {
		t.Errorf("bonus %+v", w)
	}
	if calls := invocations(t, log); len(calls) != 2 || calls[1] != "chat --no-interactive /usage" {
		t.Errorf("ran %q", calls)
	}
}

// A kiro-cli that is not signed in is never asked for usage.
func TestKiroNotSignedInIsErrAuth(t *testing.T) {
	bin, log := fakeCLI(t, map[string]fakeAnswer{"whoami": failed("Not logged in\n")})
	if _, err := (Kiro{Bin: bin}).Read(context.Background(), ""); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
	if calls := invocations(t, log); len(calls) != 1 {
		t.Errorf("ran %q", calls)
	}
	portal := "Failed to initialize auth portal.\nPlease try again with: kiro-cli login --use-device-flow\nerror: OAuth error: All callback ports are in use."
	bin, _ = fakeCLI(t, map[string]fakeAnswer{"whoami": ok("Logged in with Builder ID\n"), "chat": failed(portal)})
	if _, err := (Kiro{Bin: bin}).Read(context.Background(), ""); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("auth portal: %v", err)
	}
}

func TestKiroNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KIRO_CLI_PATH", "")
	if _, err := (Kiro{}).Read(context.Background(), ""); !errors.Is(err, usage.ErrNotInstalled) {
		t.Errorf("err %v", err)
	}
}

func TestKiroReportShapes(t *testing.T) {
	legacy := "| KIRO FREE                                          |\n" +
		"████████████████████████████████████████████████████ 25%\n" +
		"(12.50 of 50 covered in plan), resets on 01/15\n"
	got, err := parseKiroUsage(legacy, kiroNow)
	if err != nil || len(got.Windows) != 1 || got.Windows[0].UsedPct != 25 ||
		!got.Windows[0].ResetsAt.Equal(time.Date(2027, 1, 15, 0, 0, 0, 0, time.Local)) {
		t.Errorf("legacy %+v, %v", got, err)
	}
	ansi := "\x1b[1mEstimated Usage\x1b[0m | resets on 2026-06-01 | \x1b[mKIRO PRO\x1b[0m\n" +
		"Credits (1000.00 of 1000 covered in plan)\n████████ 100%\n"
	if got, err := parseKiroUsage(stripANSI(ansi), kiroNow); err != nil || got.Windows[0].UsedPct != 100 {
		t.Errorf("ansi %+v, %v", got, err)
	}
	creditsOnly := "Credits (12.5 of 50 covered in plan)"
	if got, err := parseKiroUsage(creditsOnly, kiroNow); err != nil || got.Windows[0].UsedPct != 25 {
		t.Errorf("credits only %+v, %v", got, err)
	}
	for _, planOnly := range []string{"Plan: Q Developer Pro\nYour plan is managed by admin", "Plan: KIRO PRO MAX | 1 usage breakdowns"} {
		if got, err := parseKiroUsage(planOnly, kiroNow); err != nil || !got.Empty() {
			t.Errorf("%q: %+v, %v", planOnly, got, err)
		}
	}
	for _, bad := range []string{
		"", "Plan: KIRO PRO MAX", "Plan: Q Developer Pro\nTip: to see context window usage, run /context",
		"⚠️  Warning: Could not retrieve usage information from backend\nError: dispatch failure (io error): an i/o error occurred",
	} {
		if _, err := parseKiroUsage(bad, kiroNow); err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if strings.Contains(stripANSI(ansi), "\x1b") {
		t.Error("escape left")
	}
}

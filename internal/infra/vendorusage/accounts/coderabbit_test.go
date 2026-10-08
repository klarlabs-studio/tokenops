package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestCodeRabbitCountsReviews(t *testing.T) {
	bin, log := fakeCLI(t, map[string]fakeAnswer{"usage": ok(readFixture(t, "coderabbit.txt"))})
	got, err := CodeRabbit{Bin: bin}.Read(context.Background(), "")
	if err != nil || len(got.Counts) != 1 || len(got.Windows) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if c := got.Counts[0]; c.Name != "reviews" || c.Used != 25 || !c.ResetsAt.Equal(time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local)) {
		t.Errorf("count %+v", c)
	}
	if calls := invocations(t, log); len(calls) != 1 || calls[0] != "usage" {
		t.Errorf("ran %q", calls)
	}
}

func TestCodeRabbitSignedOut(t *testing.T) {
	bin, _ := fakeCLI(t, map[string]fakeAnswer{"usage": failed("Not authenticated. Run coderabbit auth login.\n")})
	if _, err := (CodeRabbit{Bin: bin}).Read(context.Background(), ""); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
	// A failing CLI is a failure, whatever it printed.
	bin, _ = fakeCLI(t, map[string]fakeAnswer{"usage": {output: "Your reviews: 99", status: "9"}})
	if _, err := (CodeRabbit{Bin: bin}).Read(context.Background(), ""); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("exit 9: %v", err)
	}
	t.Setenv("CODERABBIT_CLI_PATH", t.TempDir()+"/missing")
	if _, err := (CodeRabbit{}).Read(context.Background(), ""); !errors.Is(err, usage.ErrNotInstalled) {
		t.Errorf("a missing override: %v", err)
	}
}

func TestCodeRabbitReportShapes(t *testing.T) {
	blank := "Organization :\nUsage billing : inactive\nUser :\nYour reviews : 0\nPlan :\nPeriod resets : 2026-09-30 14:30:00\n"
	got, err := parseCodeRabbitUsage(blank)
	if err != nil || len(got.Counts) != 1 || got.Counts[0].Used != 0 ||
		!got.Counts[0].ResetsAt.Equal(time.Date(2026, 9, 30, 14, 30, 0, 0, time.Local)) {
		t.Errorf("blank fields %+v, %v", got, err)
	}
	if got, err := parseCodeRabbitUsage(stripANSI("\x1b[32mYour reviews\x1b[0m: 12\r\nUsage billing: active\r\n")); err != nil || got.Counts[0].Used != 12 {
		t.Errorf("ansi %+v, %v", got, err)
	}
	if _, err := parseCodeRabbitUsage("Please log in using coderabbit auth login."); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("signed out: %v", err)
	}
	for _, bad := range []string{"Plan: Pro", "Organization: Example\nUser: example-user", "unexpected output",
		"Your reviews: -1", "Your reviews: 999999999999999999999999999999999"} {
		if _, err := parseCodeRabbitUsage(bad); err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

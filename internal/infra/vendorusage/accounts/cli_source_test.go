package accounts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestDoubaoCLIReadsTheCodingPlan(t *testing.T) {
	bin, log := fakeCLI(t, map[string]fakeAnswer{"usage": ok(readFixture(t, "doubao-cli.json"))})
	r := readerDoubaoCLI().(cliSource)
	r.bin = bin
	got, err := r.Read(context.Background(), "")
	if err != nil || !got.Subscription || len(got.Windows) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 7.48) || w.ResetsAt.IsZero() {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[2]; w.Name != "month" || !approx(w.UsedPct, 1.36) {
		t.Errorf("month %+v", w)
	}
	if calls := invocations(t, log); len(calls) != 1 || calls[0] != "usage plan --format json" {
		t.Errorf("ran %q", calls)
	}
}

func TestDoubaoCLIAgentPlanAndRefusals(t *testing.T) {
	agent := `{"viewer":{"auth_method":"sso"},"items":[{"product":"agent-plan","subscribed":true,"periods":[{"label":"weekly","percent":28.7}]},{"product":"coding-plan","subscribed":false,"periods":[{"label":"weekly","percent":99}]}]}`
	bin, _ := fakeCLI(t, map[string]fakeAnswer{"usage": ok(agent)})
	r := readerDoubaoCLI().(cliSource)
	r.bin = bin
	if got, err := r.Read(context.Background(), ""); err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 28.7) {
		t.Errorf("agent plan: %+v %v", got, err)
	}
	for name, answer := range map[string]fakeAnswer{
		"no sign-in":    ok(`{"viewer":{"auth_method":"none"},"items":[]}`),
		"says so":       failed("Error: not logged in. Run arkcli auth login\n"),
		"no plan":       ok(`{"viewer":{"auth_method":"sso"},"items":[]}`),
		"not json":      ok("token=SECRET-IN-OUTPUT\n"),
		"exits failing": failed("boom SECRET-IN-OUTPUT"),
	} {
		bin, _ := fakeCLI(t, map[string]fakeAnswer{"usage": answer})
		r.bin = bin
		_, err := r.Read(context.Background(), "")
		if err == nil || strings.Contains(err.Error(), "SECRET-IN-OUTPUT") {
			t.Errorf("%s: %v", name, err)
		}
		if signedOut := name == "no sign-in" || name == "says so"; signedOut != errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s: ErrAuth %v: %v", name, signedOut, err)
		}
	}
}

// A CLI that is not installed is skipped silently, whatever is on PATH:
// the override names where it is.
func TestCLISourceNotInstalled(t *testing.T) {
	t.Setenv("ARKCLI_PATH", filepath.Join(t.TempDir(), "missing"))
	if _, err := readerDoubaoCLI().Read(context.Background(), ""); !errors.Is(err, usage.ErrNotInstalled) {
		t.Errorf("%v", err)
	}
}

// bl gets only the environment it needs, and the mainland console is
// asked when the international one does not answer.
func TestAlibabaTokenPlanEnvironmentAndFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}
	dir := t.TempDir()
	envLog, argLog := filepath.Join(dir, "env.log"), filepath.Join(dir, "args.log")
	out := filepath.Join(dir, "out.json")
	if err := os.WriteFile(out, []byte(readFixture(t, "alibabatokenplan-cli.json")), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nenv >> '" + envLog + "'\necho \"$@\" >> '" + argLog + "'\n" +
		"case \"$*\" in *international*) echo 'InvalidSite'; exit 1 ;; esac\ncat '" + out + "'\n"
	bin := filepath.Join(dir, "bl")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // a test executable
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "sk-must-not-leak")
	t.Setenv("LANG", "en_US.UTF-8")
	r := readerAlibabaTokenPlanCLI().(cliSource)
	r.bin = bin
	got, err := r.Read(context.Background(), "")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 25) || !w.ResetsAt.Equal(time.UnixMilli(1787000400000).UTC()) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 70) {
		t.Errorf("week %+v", w)
	}
	env, _ := os.ReadFile(envLog)
	if strings.Contains(string(env), "sk-must-not-leak") || !strings.Contains(string(env), "LANG=en_US.UTF-8") {
		t.Errorf("environment:\n%s", env)
	}
	if calls := invocations(t, argLog); len(calls) != 2 || !strings.Contains(calls[1], "cn-beijing --console-site domestic --output json") {
		t.Errorf("ran %q", calls)
	}
}

func TestTokenPlanCLIShapes(t *testing.T) {
	for body, windows := range map[string]int{
		`{"per5HourPercentage":0.25,"per1MonthPercentage":0.5,"per1MonthResetTime":1787000400000}`: 2,
		"notice: update available\n" + `{"data":{"per1WeekPercentage":0.1}}`:                       1,
		`{"data":{"per1WeekPercentage":true}}`:                                                     0,
		`["not","an","object"]`:                                                                    0,
	} {
		got, err := parseTokenPlanCLI(body)
		if len(got.Windows) != windows || (windows == 0) != (err != nil) {
			t.Errorf("%s: %+v %v", body, got, err)
		}
	}
}

package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	r := readerAlibabaTokenPlanCLI().(alibabaTokenPlanCLI)
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
	if calls := invocations(t, argLog); len(calls) != 2 || !strings.Contains(calls[1], "console call --api zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage --data {} --console-region cn-beijing --console-site domestic --output json") {
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

// A Team plan answers no personal windows: its seats are listed page by
// page, with the region in BAILIAN_BASE_URL, and the credits of the seats
// in service added up (CodexBar 53f9e1e24d). A suspended seat, one out of
// its term and resource packs are not added.
func TestAlibabaTokenPlanTeamSeats(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}
	dir := t.TempDir()
	seat := func(id, status string, total, left int) string {
		return `{"SeatId":"` + id + `","Status":"` + status + `","StartTime":1780000000000,"EndTime":1790000000000,"EquityList":[` +
			`{"EquityType":"CREDITS","CycleStartTime":1785000000000,"CycleEndTime":1787000000000,"CycleTotalValue":` + strconv.Itoa(total) + `,"CycleSurplusValue":` + strconv.Itoa(left) + `},` +
			`{"EquityType":"RESOURCE_PACK","CycleTotalValue":999,"CycleSurplusValue":999}]}`
	}
	first := make([]string, 0, 10)
	for i := range 10 {
		first = append(first, seat("s"+strconv.Itoa(i), "NORMAL", 100, 75))
	}
	pages := map[string]string{
		"1": `{"Success":true,"Data":{"Total":12,"PageNo":1,"PageSize":10,"Items":[` + strings.Join(first, ",") + `]}}`,
		"2": `{"Success":true,"Data":{"Total":12,"PageNo":2,"PageSize":10,"Items":[` + seat("s10", "NORMAL", 200, 0) + `,` + seat("s11", "SUSPENDED", 500, 0) + `]}}`,
	}
	for n, body := range pages {
		if err := os.WriteFile(filepath.Join(dir, "page"+n+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	envLog := filepath.Join(dir, "env.log")
	script := "#!/bin/sh\n" +
		"case \"$1\" in console) echo '{\"code\":\"NoPermission\"}'; exit 1 ;; esac\n" +
		"echo \"$BAILIAN_BASE_URL\" >> '" + envLog + "'\n" +
		"case \"$BAILIAN_BASE_URL\" in *intl*) exit 1 ;; esac\n" +
		"cat '" + dir + "'/page\"$4\".json\n"
	bin := filepath.Join(dir, "bl")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // a test executable
		t.Fatal(err)
	}
	r := readerAlibabaTokenPlanCLI().(alibabaTokenPlanCLI)
	r.bin = bin
	r.now = func() time.Time { return time.UnixMilli(1786000000000) }
	got, err := r.Read(context.Background(), "")
	// 10 seats of 100 with 75 left, and one of 200 with none: 450 used of 1200.
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != "credits" || !approx(got.Windows[0].UsedPct, 37.5) ||
		!got.Windows[0].ResetsAt.Equal(time.UnixMilli(1787000000000).UTC()) || got.Scope != "team" {
		t.Fatalf("%+v %v", got, err)
	}
	if env, _ := os.ReadFile(envLog); !strings.Contains(string(env), "https://dashscope-intl.aliyuncs.com\nhttps://dashscope.aliyuncs.com") {
		t.Errorf("regions asked:\n%s", env)
	}
}

func TestAlibabaTokenPlanTeamSeatShapes(t *testing.T) {
	now := time.UnixMilli(1786000000000)
	page := func(items string) []tokenPlanSeatsPage {
		var p tokenPlanSeatsPage
		if err := json.Unmarshal([]byte(`{"Success":true,"Data":{"Total":1,"PageNo":1,"PageSize":10,"Items":[`+items+`]}}`), &p); err != nil {
			t.Fatal(err)
		}
		return []tokenPlanSeatsPage{p}
	}
	for name, items := range map[string]string{
		"two credits":     `{"SeatId":"a","Status":"NORMAL","StartTime":1,"EndTime":1790000000000,"EquityList":[{"EquityType":"CREDITS","CycleStartTime":1,"CycleEndTime":1790000000000,"CycleTotalValue":1,"CycleSurplusValue":1},{"EquityType":"CREDITS","CycleStartTime":1,"CycleEndTime":1790000000000,"CycleTotalValue":1,"CycleSurplusValue":1}]}`,
		"left > total":    `{"SeatId":"a","Status":"NORMAL","StartTime":1,"EndTime":1790000000000,"EquityList":[{"EquityType":"CREDITS","CycleStartTime":1,"CycleEndTime":1790000000000,"CycleTotalValue":1,"CycleSurplusValue":2}]}`,
		"no seat id":      `{"SeatId":"","Status":"NORMAL"}`,
		"none in service": `{"SeatId":"a","Status":"EXPIRED"}`,
	} {
		if _, err := tokenPlanTeamReading(page(items), now); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}

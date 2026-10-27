package accounts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// jetbrainsHome lays out a macOS home with one IDE's quota file.
func jetbrainsHome(t *testing.T, ide, xml string, modified time.Time) string {
	t.Helper()
	home := t.TempDir()
	p := filepath.Join(home, "Library", "Application Support", "JetBrains", ide, "options", jetbrainsQuotaFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, modified, modified); err != nil {
		t.Fatal(err)
	}
	return home
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The monthly tariff is the window; top-up credits do not dilute it.
func TestJetBrainsReadsTheMonthlyTariff(t *testing.T) {
	home := jetbrainsHome(t, "GoLand2026.2", readFixture(t, "jetbrains.xml"), time.Now())
	got, err := JetBrains{Home: home, GOOS: "darwin"}.Read(context.Background(), "")
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	w := got.Windows[0]
	if w.Name != "month" || !approx(w.UsedPct, 34.6) || w.Duration != 720*time.Hour ||
		!w.ResetsAt.Equal(time.Date(2026, 1, 16, 14, 0, 54, 939e6, time.UTC)) {
		t.Errorf("window %+v", w)
	}
}

func TestJetBrainsWithoutAnIDEIsNotInstalled(t *testing.T) {
	if _, err := (JetBrains{Home: t.TempDir(), GOOS: "darwin"}).Read(context.Background(), ""); !errors.Is(err, usage.ErrNotInstalled) {
		t.Errorf("err %v", err)
	}
}

func TestJetBrainsShapes(t *testing.T) {
	const totalOnly = `<application><component name="AIAssistantQuotaManager2"><option name="quotaInfo" value="{&#10;  &quot;type&quot;: &quot;paid&quot;,&#10;  &quot;current&quot;: &quot;50000&quot;,&#10;  &quot;maximum&quot;: &quot;100000&quot;,&#10;  &quot;until&quot;: &quot;2025-12-31T23:59:59Z&quot;&#10;}" /></component></application>`
	got, err := JetBrains{Home: jetbrainsHome(t, "IntelliJIdea2026.1", totalOnly, time.Now()), GOOS: "darwin"}.Read(context.Background(), "")
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 50) || got.Windows[0].Name != "month" {
		t.Errorf("total only: %+v, %v", got, err)
	}
	for _, xml := range []string{
		`<application><component name="AIAssistantQuotaManager2"><option name="quotaInfo" value="" /></component></application>`,
		`<application><component name="Other"><option name="quotaInfo" value="{}" /></component></application>`,
		`not xml`,
	} {
		if _, err := (JetBrains{Home: jetbrainsHome(t, "PyCharm2026.1", xml, time.Now()), GOOS: "darwin"}).Read(context.Background(), ""); err == nil {
			t.Errorf("%q read without error", xml)
		}
	}
}

// idea.log, newer than the XML, replaces quota and refill together.
func TestJetBrainsPrefersANewerLoggedQuota(t *testing.T) {
	home := jetbrainsHome(t, "WebStorm2026.2", readFixture(t, "jetbrains.xml"), time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local))
	log := "2026-10-05 15:21:27,386 [1]   INFO - #c.i.m.l.c.q.QuotaManager2Impl - New quota refill state is: Known(next=2026-10-11T17:00:30.231Z, tariff=QuotaRefillInfoTariff(amount=1000000, duration=30d))\n" +
		"2026-10-05 15:27:49,811 [2]   INFO - #c.i.m.l.c.q.QuotaManager2Impl - New quota state is: Available(current=346495.294, maximum=6489986.397, until=2028-09-22T21:00:00Z, tariffQuota=QuotaDetails(current=346495.294, maximum=1000000, available=653504.706), topUpQuota=QuotaDetails(current=0, maximum=5489986.397, available=5489986.397))\n"
	p := filepath.Join(home, "Library", "Logs", "JetBrains", "WebStorm2026.2", "idea.log")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := JetBrains{Home: home, GOOS: "darwin"}.Read(context.Background(), "")
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; !approx(w.UsedPct, 34.6495294) || w.Duration != 30*24*time.Hour ||
		!w.ResetsAt.Equal(time.Date(2026, 10, 11, 17, 0, 30, 231e6, time.UTC)) {
		t.Errorf("window %+v", w)
	}
	// A log line being written is not read.
	if err := os.WriteFile(p, []byte(log[:len(log)-1]), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ = JetBrains{Home: home, GOOS: "darwin"}.Read(context.Background(), "")
	if w := got.Windows[0]; !approx(w.UsedPct, 34.6) {
		t.Errorf("unfinished log used: %+v", w)
	}
}

// writeJetBrainsLog writes idea.log for the WebStorm installation in home.
func writeJetBrainsLog(t *testing.T, home, log string) {
	t.Helper()
	p := filepath.Join(home, "Library", "Logs", "JetBrains", "WebStorm2026.2", "idea.log")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Invented values only: the identifiers below are placeholders.
const (
	jbaiStateLine  = "2026-10-20 10:00:00,000 [1]   INFO - #c.i.m.l.c.q.QuotaManager2Impl - New quota state is: Available(quota=JbaiQuota(license=TEST-LICENSE-1, current=2500, maximum=3000000, quotaID=JbaiQuotaId(quotaId=TEST-QUOTA-1, userId=null), until=2028-01-01T00:00:00Z, tariffQuota=Details(current=2500, maximum=1000000, available=997500), topUpQuota=Details(current=0, maximum=2000000, available=2000000)))\n"
	jbaiRefillLine = "2026-10-20 10:00:01,000 [1]   INFO - #c.i.m.l.c.q.QuotaManager2Impl - New quota refill state is: Known(refill=JbaiQuotaRefill(tariff=JbaiQuotaTariff(amount=1000000, period=30d), next=2026-11-10T17:00:00.000Z, last=2026-10-11T17:00:00.000Z))\n"
	junieLine      = `2026-10-20 11:00:00,000 [2]   INFO - AiaActivationTransmitter - Received message from junie: quota_update:{"license":"TEST-LICENSE-2","current":{"amount":"5000"},"maximum":{"amount":"3000000.0"},"until":1900000000000,"tariffQuota":{"current":{"amount":"5000"},"maximum":{"amount":"1000000.0"},"available":{"amount":"995000"}},"topUpQuota":{"current":{"amount":"0"},"maximum":{"amount":"2000000.0"},"available":{"amount":"1500000"}}}` + "\n"
	disabledLine   = "2026-10-20 12:00:00,000 [3]   INFO - #c.i.m.l.c.q.QuotaManager2Impl - New quota state is: Error(exception=com.intellij.ml.llm.core.AIAssistantForbiddenException: Usage of AI Assistant is prohibited by machine-wide settings (AirMigration))\n"
	unknownLine    = "2026-10-20 13:00:00,000 [4]   INFO - #c.i.m.l.c.q.QuotaManager2Impl - New quota state is: Available(quota=JbaiQuotaV3(license=TEST-LICENSE-3, mystery=1))\n"
)

// readWebStorm reads the WebStorm installation whose XML is dated 2026-09-01.
func readWebStorm(t *testing.T, log string) (usage.Reading, error) {
	t.Helper()
	home := jetbrainsHome(t, "WebStorm2026.2", readFixture(t, "jetbrains.xml"), time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local))
	if log != "" {
		writeJetBrainsLog(t, home, log)
	}
	return JetBrains{Home: home, GOOS: "darwin"}.Read(context.Background(), "")
}

// The JbaiQuota layout AI Assistant 262.x logs, with its refill.
func TestJetBrainsReadsTheJbaiQuotaLayout(t *testing.T) {
	got, err := readWebStorm(t, jbaiStateLine+jbaiRefillLine)
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	w := got.Windows[0]
	if !approx(w.UsedPct, 0.25) || w.Duration != 30*24*time.Hour ||
		!w.ResetsAt.Equal(time.Date(2026, 11, 10, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("window %+v", w)
	}
	if !got.HasCredits || !approx(got.Credits, 2000000) || got.CreditsUnit != "credits" {
		t.Errorf("top-up balance %+v", got)
	}
	if got.Notice != "" {
		t.Errorf("notice %q on a readable log", got.Notice)
	}
}

// Junie's quota_update JSON carries the quota with no refill state.
func TestJetBrainsReadsJunieQuotaUpdates(t *testing.T) {
	got, err := readWebStorm(t, junieLine)
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; !approx(w.UsedPct, 0.5) || w.Duration != 30*24*time.Hour || !w.ResetsAt.IsZero() {
		t.Errorf("window %+v", w)
	}
	if !got.HasCredits || !approx(got.Credits, 1500000) {
		t.Errorf("top-up balance %+v", got)
	}
}

// AI Assistant disabled by Air logs an error between Junie's updates; that
// error says nothing about the account and must not hide Junie's record.
func TestJetBrainsSkipsADisabledAssistantState(t *testing.T) {
	got, err := readWebStorm(t, junieLine+disabledLine)
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 0.5) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got.Notice != "" {
		t.Errorf("notice %q for a known disabled state", got.Notice)
	}
}

// A newest quota line in an unknown layout is not read: the quota file is
// used, and health says so without the identifiers in the line.
func TestJetBrainsNamesStaleQuotaWhenTheNewestLineIsUnknown(t *testing.T) {
	got, err := readWebStorm(t, junieLine+unknownLine)
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 34.6) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if !strings.HasPrefix(got.Notice, "using stale quota from ") || !strings.Contains(got.Notice, "WebStorm2026.2") {
		t.Errorf("notice %q", got.Notice)
	}
	for _, leak := range []string{"TEST-LICENSE", "TEST-QUOTA"} {
		if strings.Contains(got.Notice, leak) {
			t.Errorf("notice leaks %q: %s", leak, got.Notice)
		}
	}
}

// A quota file with no numbers in a shape this version cannot read is an
// error, never a successful empty reading.
func TestJetBrainsQuotaWithoutNumbersIsAnError(t *testing.T) {
	const noNumbers = `<application><component name="AIAssistantQuotaManager2"><option name="quotaInfo" value="{&quot;type&quot;: &quot;paid&quot;}" /></component></application>`
	if _, err := (JetBrains{Home: jetbrainsHome(t, "PyCharm2026.1", noNumbers, time.Now()), GOOS: "darwin"}).Read(context.Background(), ""); err == nil {
		t.Error("a quota with no numbers read without error")
	}
}

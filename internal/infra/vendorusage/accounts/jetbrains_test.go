package accounts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

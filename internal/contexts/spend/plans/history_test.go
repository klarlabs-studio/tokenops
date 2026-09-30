package plans

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func day(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }

func TestHistoryWithoutChangesKeepsTheConfiguredPlan(t *testing.T) {
	var h History
	if got := h.At("openai", day(3), "gpt-plus"); got != "gpt-plus" {
		t.Fatalf("At = %q", got)
	}
	got := h.Periods("openai", day(1), day(11), "gpt-plus")
	if want := []Period{{Plan: "gpt-plus", From: day(1), To: day(11)}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Periods = %+v", got)
	}
}

// A switch in the middle of a month splits it, and the first switch
// records the plan that was in force before it.
func TestSwitchSplitsThePeriod(t *testing.T) {
	h := History(History(nil).Switch("openai", "gpt-plus", "gpt-pro-5x", day(15), day(15)))
	if got := h.At("openai", day(3), "gpt-pro-5x"); got != "gpt-plus" {
		t.Errorf("before the switch: %q, want gpt-plus", got)
	}
	if got := h.At("openai", day(20), "gpt-pro-5x"); got != "gpt-pro-5x" {
		t.Errorf("after the switch: %q", got)
	}
	got := h.Periods("openai", day(1), day(30), "gpt-pro-5x")
	want := []Period{{Plan: "gpt-plus", From: day(1), To: day(15)}, {Plan: "gpt-pro-5x", From: day(15), To: day(30)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Periods = %+v", got)
	}
}

// Unsetting records an empty plan, and the time without one is dropped.
func TestUnsetLeavesAGap(t *testing.T) {
	h := make(History, 0, 3)
	h = append(h, h.Switch("openai", "", "gpt-plus", day(1), day(1))...)
	h = append(h, h.Switch("openai", "gpt-plus", "", day(10), day(10))...)
	h = append(h, h.Switch("openai", "", "gpt-plus", day(20), day(20))...)
	got := h.Periods("openai", day(1), day(30), "gpt-plus")
	want := []Period{{Plan: "gpt-plus", From: day(1), To: day(10)}, {Plan: "gpt-plus", From: day(20), To: day(30)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Periods = %+v", got)
	}
}

// A backdated correction recorded later wins over what it corrects.
func TestLaterRecordingWinsATie(t *testing.T) {
	h := History{
		{Provider: "openai", Plan: "gpt-plus", From: day(1), Recorded: day(1)},
		{Provider: "openai", Plan: "gpt-pro-5x", From: day(1), Recorded: day(30)},
	}
	if got := h.At("openai", day(5), ""); got != "gpt-pro-5x" {
		t.Fatalf("At = %q", got)
	}
}

func TestPeriodCost(t *testing.T) {
	// A whole average month costs the monthly price.
	p := Period{From: day(1), To: day(1).Add(time.Duration(averageMonthDays * 24 * float64(time.Hour)))}
	if got := PeriodCost(100, p); math.Abs(got-100) > 0.01 {
		t.Fatalf("PeriodCost = %.4f", got)
	}
	if got := PeriodCost(100, Period{From: day(1), To: day(1)}); got != 0 {
		t.Fatalf("empty period costs %.4f", got)
	}
}

func TestCatalogPricesArePinned(t *testing.T) {
	for _, name := range Names() {
		p, _ := Lookup(name)
		if p.MonthlyUSD > 0 && p.PriceSource == "" {
			t.Errorf("%s has a price with no source", name)
		}
	}
	if p, _ := Lookup("claude-max-20x"); p.MonthlyUSD != 200 {
		t.Errorf("claude-max-20x = %v (relative tiers must keep their own price)", p.MonthlyUSD)
	}
	if p, _ := Lookup("gpt-pro-5x"); p.MonthlyUSD != 100 {
		t.Errorf("gpt-pro-5x = %v", p.MonthlyUSD)
	}
}

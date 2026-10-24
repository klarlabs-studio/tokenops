package coachhook

import (
	"strings"
	"testing"
	"time"
)

// The status line words the open tip from the session's state, so a
// quota tip keeps which window it was about and when that window resets.
func TestOpenTipCarriesTheQuotaWindow(t *testing.T) {
	dir := t.TempDir()
	cfg := tipConfig()
	cfg.Quota = burningQuota(60, 2*time.Hour, 80*time.Minute)
	tp := writeTranscript(t, dir, turnLine(ts(1), 60_000_000, opus))
	d := Evaluate(dir, "s1", tp, cfg, fixedNow)
	if d.OfferID == "" {
		t.Fatalf("no tip offered: %+v", d)
	}
	tip, ok := OpenTip(dir, "s1")
	if !ok {
		t.Fatal("no open tip read back")
	}
	if tip.ID != d.OfferID || tip.Kind != "quota_burn" || tip.Window != "Claude 5-hour" ||
		!tip.ResetsAt.Equal(fixedNow.Add(2*time.Hour)) || !tip.RunsOutAt.Equal(fixedNow.Add(80*time.Minute)) {
		t.Fatalf("open tip = %+v", tip)
	}
	if _, ok := OpenTip(dir, "other"); ok {
		t.Error("another session's tip leaked")
	}
	if _, ok := OpenTip(dir, ""); ok {
		t.Error("an empty session id read a tip")
	}
}

func TestOpenTipWordsShortly(t *testing.T) {
	now := fixedNow
	cases := []struct {
		tip  Tip
		want string
	}{
		{Tip{Kind: "compact_now"}, "compact now (/compact)"},
		{Tip{Kind: "budget_over"}, "past the session budget → fresh session"},
		{Tip{Kind: "budget_50"}, "session budget 50% left"},
		{Tip{Kind: "quota_75", Window: "Claude weekly", ResetsAt: now.Add(94 * time.Hour)}, "Claude weekly 25% left → /compact, smaller model"},
		{Tip{Kind: "quota_100", Window: "OpenAI weekly", ResetsAt: now.Add(94 * time.Hour)}, "OpenAI weekly out for 3d22h → use another plan"},
		{Tip{Kind: "quota_90"}, "plan window 10% left → /compact, smaller model"},
		{Tip{Kind: "quota_burn", Window: "Claude 5-hour", ResetsAt: now.Add(2 * time.Hour), RunsOutAt: now.Add(80 * time.Minute)}, "Claude 5-hour out in ~1h20m → smaller model"},
		{Tip{Kind: "quota_burn", Window: "Claude weekly Fable", ResetsAt: now.Add(30 * time.Hour), RunsOutAt: now.Add(28 * time.Hour)}, "Claude weekly Fable out in ~1d4h → smaller model"},
		{Tip{Kind: "quota_burn", Window: "Claude 5-hour", ResetsAt: now.Add(time.Hour), RunsOutAt: now.Add(-time.Minute)}, "Claude 5-hour about to run out → smaller model"},
		{Tip{Kind: "quota_burn", Window: "Claude 5-hour", ResetsAt: now.Add(-time.Minute), RunsOutAt: now.Add(-time.Hour)}, ""},
		{Tip{Kind: "quota_75", Window: "Claude weekly", ResetsAt: now.Add(-time.Minute)}, ""},
		{Tip{}, ""},
	}
	for _, c := range cases {
		got := c.tip.Short(now)
		if got != c.want {
			t.Errorf("%+v: Short = %q; want %q", c.tip, got, c.want)
		}
		if len([]rune(got)) > 50 {
			t.Errorf("%q is too long for a status line", got)
		}
	}
}

// Quiet only speaks when work is about to stop, so only those tips are
// urgent: the spent budget and a window running out or used up.
func TestOpenTipUrgency(t *testing.T) {
	for kind, want := range map[string]bool{
		"budget_over": true, "quota_90": true, "quota_100": true, "quota_burn": true,
		"budget_50": false, "budget_75": false, "quota_50": false, "quota_75": false, "compact_now": false, "": false,
	} {
		if got := (Tip{Kind: kind}).Urgent(); got != want {
			t.Errorf("%q urgent = %v; want %v", kind, got, want)
		}
	}
}

// A tip counts as seen once per tip: the status line refreshes every few
// seconds and must not write the ledger on each refresh.
func TestMarkSeenOncePerTip(t *testing.T) {
	dir := t.TempDir()
	if !MarkSeen(dir, "s1", "tip-1") {
		t.Fatal("first showing not marked")
	}
	if MarkSeen(dir, "s1", "tip-1") {
		t.Error("the same tip was marked twice")
	}
	if !MarkSeen(dir, "s1", "tip-2") {
		t.Error("a new tip was not marked")
	}
	if !MarkSeen(dir, "s2", "tip-2") || MarkSeen(dir, "", "x") || MarkSeen(dir, "s1", "") {
		t.Error("marks are per session and need both ids")
	}
}

// The status line marks a tip seen in a file of its own: the hook
// rewrites its state file whole on every Stop and would drop the mark.
func TestMarkSeenLeavesTheHookStateAlone(t *testing.T) {
	dir := t.TempDir()
	saveSession(dir, "s1", sessionState{OpenTip: &openTip{ID: "tip-1", Kind: "compact_now"}})
	MarkSeen(dir, "s1", "tip-1")
	if tip, ok := OpenTip(dir, "s1"); !ok || tip.ID != "tip-1" {
		t.Fatalf("hook state changed: %+v %v", tip, ok)
	}
	if !strings.HasPrefix(seenFile(dir, "s1"), dir) || seenFile(dir, "s1") == sessionFile(dir, "s1") {
		t.Errorf("seen mark at %q", seenFile(dir, "s1"))
	}
}

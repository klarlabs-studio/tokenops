package coach

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

// stubRefused replaces the read guard's ledger for a test: refused
// re-reads and their tokens from since on.
func stubRefused(t *testing.T, fn func(since time.Time) (int, int64)) {
	t.Helper()
	prev := RefusedRereads
	t.Cleanup(func() { RefusedRereads = prev })
	RefusedRereads = fn
}

func move(at time.Time, power, kind string, channel ft.Channel) ft.Entry {
	return ft.Entry{Type: ft.EntryOffer, ID: at.String() + kind, At: at, Power: power, Channel: channel, Kind: kind, From: "opus", To: "sonnet"}
}

// What the coach did is counted per period from its own ledgers: the
// subagents it moved, the default model it lowered and the re-reads the
// guard refused, today and over the last week. Proposals it only made
// (approvals) and other powers' changes are not moves of a model.
func TestActivityCountsWhatTheCoachDidTodayAndThisWeek(t *testing.T) {
	at := time.Date(2026, 10, 26, 15, 0, 0, 0, time.Local)
	midnight := time.Date(2026, 10, 26, 0, 0, 0, 0, time.Local)
	l := &memLedger{entries: []ft.Entry{
		move(at.Add(-time.Hour), config.PowerModels, "implementation", ft.ChannelMove),
		move(at.Add(-2*time.Hour), config.PowerModels, "lookup", ft.ChannelMove),
		move(at.Add(-3*time.Hour), config.PowerModels, DefaultModelKind, ft.ChannelMove),
		move(at.Add(-30*time.Hour), config.PowerModels, "lookup", ft.ChannelMove),
		move(at.Add(-10*24*time.Hour), config.PowerModels, "lookup", ft.ChannelMove),
		move(at.Add(-time.Hour), config.PowerModels, "debugging", ft.ChannelApproval),
		move(at.Add(-time.Hour), config.PowerContext, "claude-code", ft.ChannelMove),
	}}
	stubRefused(t, func(since time.Time) (int, int64) {
		if since.Equal(midnight) {
			return 5, 12_000
		}
		if since.Equal(at.Add(-ActivityDays * 24 * time.Hour)) {
			return 258, 1_200_000
		}
		t.Errorf("unexpected since %v", since)
		return 0, 0
	})
	got := Activities(l, at)
	if len(got) != 2 {
		t.Fatalf("activity = %+v", got)
	}
	today, week := got[0], got[1]
	if today.Period != PeriodToday || !today.Since.Equal(midnight) || today.SubagentMoves != 2 || today.DefaultModelMoves != 1 ||
		today.RefusedRereads != 5 || today.RefusedTokens != 12_000 {
		t.Errorf("today = %+v", today)
	}
	if today.Summary != "moved 2 subagents to a cheaper model · started new sessions on a cheaper model · refused 5 re-reads" {
		t.Errorf("today summary = %q", today.Summary)
	}
	if week.Period != PeriodWeek || week.SubagentMoves != 3 || week.DefaultModelMoves != 1 || week.RefusedRereads != 258 {
		t.Errorf("week = %+v", week)
	}
	if !strings.HasPrefix(week.Summary, "moved 3 subagents to a cheaper model") {
		t.Errorf("week summary = %q", week.Summary)
	}
}

// A day the coach did nothing has no summary, and one move is one
// subagent; nothing is invented to fill the line.
func TestActivityWithLittleOrNothingDone(t *testing.T) {
	at := time.Date(2026, 10, 26, 15, 0, 0, 0, time.Local)
	stubRefused(t, func(time.Time) (int, int64) { return 0, 0 })
	got := Activities(&memLedger{}, at)
	for _, a := range got {
		if a.Summary != "" {
			t.Errorf("%s: %q from nothing", a.Period, a.Summary)
		}
	}
	got = Activities(&memLedger{entries: []ft.Entry{move(at.Add(-time.Minute), config.PowerModels, "lookup", ft.ChannelMove)}}, at)
	if got[0].Summary != "moved 1 subagent to a cheaper model" {
		t.Errorf("summary = %q", got[0].Summary)
	}
	if got := Activities(nil, at); len(got) != 2 || got[0].Summary != "" {
		t.Errorf("no ledger: %+v", got)
	}
}

// Status carries what the coach did and what each preset means, so a
// surface explains the choice it offers from the definitions here.
func TestStatusCarriesActivityAndPresets(t *testing.T) {
	stubRefused(t, func(time.Time) (int, int64) { return 7, 9_000 })
	r := Status(config.Default(), &memLedger{}, nil, time.Now())
	if len(r.Activity) != 2 || r.Activity[0].RefusedRereads != 7 {
		t.Errorf("activity = %+v", r.Activity)
	}
	if len(r.Presets) != len(Presets()) {
		t.Fatalf("presets = %+v", r.Presets)
	}
	for i, p := range Presets() {
		if r.Presets[i].Name != p.Name || r.Presets[i].Summary != p.Summary {
			t.Errorf("preset %d = %+v, want %s: %s", i, r.Presets[i], p.Name, p.Summary)
		}
	}
}

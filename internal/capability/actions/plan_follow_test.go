package actions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/infra/planevidence"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func claudeReports(plan, tier string) []planevidence.Evidence {
	return []planevidence.Evidence{{
		Provider: "anthropic", Plan: plan, From: "Claude Code", Client: "claude_code",
		Detail: "account type claude_max, rate-limit tier " + tier,
		Facts:  map[string]string{"account_type": "claude_max", "rate_limit_tier": tier},
	}}
}

func followStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func lastSwitch(t *testing.T, provider string) (plan string, from time.Time) {
	t.Helper()
	f, err := planhistory.Default()
	if err != nil {
		t.Fatal(err)
	}
	h, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range h {
		if b.Provider == provider {
			plan, from = b.Plan, b.From
		}
	}
	return plan, from
}

// The operator moved from Max 20x to Max 5x; Claude Code says so. TokenOps
// follows it at once, records the switch with its evidence in the history
// and the audit log, and tells the operator how to undo it.
func TestFollowRebindsToTheReportedPlan(t *testing.T) {
	path := sandbox(t, "plans:\n  anthropic: claude-max-20x\n")
	store := followStore(t)
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	got, err := FollowClientPlans(context.Background(), path, claudeReports("claude-max-5x", "default_claude_max_5x"), store, now)
	if err != nil || len(got.Followed) != 1 || len(got.Kept) != 0 {
		t.Fatalf("follow = %+v, %v", got, err)
	}
	f := got.Followed[0]
	if f.Previous != "claude-max-20x" || f.Plan != "claude-max-5x" || !f.Since.Equal(now) {
		t.Errorf("followed = %+v", f)
	}
	note := f.Note()
	for _, want := range []string{"claude-max-5x", "Claude Code", "default_claude_max_5x", "`tokenops plan set anthropic claude-max-20x`"} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q lacks %q", note, want)
		}
	}
	if reload(t, path).Plans["anthropic"] != "claude-max-5x" {
		t.Error("binding not written")
	}
	if plan, from := lastSwitch(t, "anthropic"); plan != "claude-max-5x" || !from.Equal(now) {
		t.Errorf("history ends on %s from %s", plan, from)
	}
	entries, err := audit.NewRecorder(store).Query(context.Background(), audit.Filter{Action: audit.ActionPlanChange})
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit = %+v, %v", entries, err)
	}
	evidence, _ := entries[0].Details["evidence"].(map[string]any)
	if entries[0].Actor != "claude_code" || entries[0].Details["previous"] != "claude-max-20x" ||
		evidence["rate_limit_tier"] != "default_claude_max_5x" || evidence["account_type"] != "claude_max" {
		t.Errorf("audit entry = %+v", entries[0])
	}

	// Said once: the same report again changes nothing.
	again, err := FollowClientPlans(context.Background(), path, claudeReports("claude-max-5x", "default_claude_max_5x"), store, now.Add(time.Hour))
	if err != nil || len(again.Followed) != 0 || len(again.Kept) != 0 {
		t.Errorf("second pass = %+v, %v", again, err)
	}
}

// The switch takes effect from when the client first reported the plan,
// not from when TokenOps got round to applying it, and no earlier.
func TestFollowTakesEffectFromTheFirstReport(t *testing.T) {
	path := sandbox(t, "plans:\n  anthropic: claude-max-20x\n")
	first := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	reports, err := planhistory.DefaultReports()
	if err != nil {
		t.Fatal(err)
	}
	if err := reports.Save(map[string]planhistory.Report{"anthropic": {Plan: "claude-max-5x", Client: "claude_code", FirstSeen: first}}); err != nil {
		t.Fatal(err)
	}
	now := first.Add(48 * time.Hour)
	got, err := FollowClientPlans(context.Background(), path, claudeReports("claude-max-5x", "default_claude_max_5x"), followStore(t), now)
	if err != nil || len(got.Followed) != 1 || !got.Followed[0].Since.Equal(first) {
		t.Fatalf("follow = %+v, %v", got, err)
	}
	if plan, from := lastSwitch(t, "anthropic"); plan != "claude-max-5x" || !from.Equal(first) {
		t.Errorf("history ends on %s from %s", plan, from)
	}
}

// The operator corrects TokenOps after the client's report: their choice
// stands until the client reports a different plan again.
func TestFollowKeepsTheOperatorsLaterChoice(t *testing.T) {
	path := sandbox(t, "plans:\n  anthropic: claude-max-20x\n")
	store := followStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	if _, err := FollowClientPlans(ctx, path, claudeReports("claude-max-5x", "default_claude_max_5x"), store, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := SetPlan(ctx, path, PlanRequest{Provider: "anthropic", Plan: "claude-max-20x", Actor: "cli", Store: store}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	kept, err := FollowClientPlans(ctx, path, claudeReports("claude-max-5x", "default_claude_max_5x"), store, t0.Add(2*time.Hour))
	if err != nil || len(kept.Followed) != 0 || len(kept.Kept) != 1 || kept.Kept[0].Plan != "claude-max-20x" {
		t.Fatalf("follow after the operator's choice = %+v, %v", kept, err)
	}
	if reload(t, path).Plans["anthropic"] != "claude-max-20x" {
		t.Fatal("the operator's choice was overwritten")
	}
	// The client now reports another plan: that is news, and is followed.
	t3 := t0.Add(3 * time.Hour)
	moved, err := FollowClientPlans(ctx, path, []planevidence.Evidence{{
		Provider: "anthropic", Plan: "claude-pro", From: "Claude Code", Client: "claude_code", Detail: "account type claude_pro",
	}}, store, t3)
	if err != nil || len(moved.Followed) != 1 || moved.Followed[0].Plan != "claude-pro" || !moved.Followed[0].Since.Equal(t3) {
		t.Fatalf("follow after a new report = %+v, %v", moved, err)
	}
}

// A report that names no single plan (a Max account without its tier, an
// unknown plan_type) changes nothing, and does not reset when the last
// clear report started.
func TestFollowIgnoresAmbiguousReports(t *testing.T) {
	path := sandbox(t, "plans:\n  anthropic: claude-max-20x\n  openai: gpt-plus\n")
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	got, err := FollowClientPlans(context.Background(), path, []planevidence.Evidence{
		{Provider: "anthropic", From: "Claude Code", Client: "claude_code", Candidates: []string{"claude-max-5x", "claude-max-20x"}},
		{Provider: "openai", From: "Codex", Client: "codex", Detail: "plan_type galaxy"},
	}, followStore(t), now)
	if err != nil || len(got.Followed) != 0 || len(got.Kept) != 0 {
		t.Fatalf("follow = %+v, %v", got, err)
	}
	cfg := reload(t, path)
	if cfg.Plans["anthropic"] != "claude-max-20x" || cfg.Plans["openai"] != "gpt-plus" {
		t.Errorf("plans = %v", cfg.Plans)
	}
	reports, _ := planhistory.DefaultReports()
	if seen, _ := reports.Load(); len(seen) != 0 {
		t.Errorf("an ambiguous report was remembered: %+v", seen)
	}
}

// A provider with nothing bound is bound to what its client reports.
func TestFollowBindsAnUnboundProvider(t *testing.T) {
	path := sandbox(t, "listen: 127.0.0.1:7878\n")
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	got, err := FollowClientPlans(context.Background(), path, claudeReports("claude-max-5x", "default_claude_max_5x"), followStore(t), now)
	if err != nil || len(got.Followed) != 1 || got.Followed[0].Previous != "" {
		t.Fatalf("follow = %+v, %v", got, err)
	}
	if !strings.Contains(got.Followed[0].Note(), "`tokenops plan set anthropic <plan>`") {
		t.Errorf("note = %q", got.Followed[0].Note())
	}
}

// The glance says what TokenOps followed for a week, while it is still
// the binding; an operator's correction ends it at once.
func TestRecentPlanFollowsAreNotedForAWeek(t *testing.T) {
	path := sandbox(t, "plans:\n  anthropic: claude-max-20x\n")
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	if _, err := FollowClientPlans(context.Background(), path, claudeReports("claude-max-5x", "default_claude_max_5x"), followStore(t), now); err != nil {
		t.Fatal(err)
	}
	bound := map[string]string{"anthropic": "claude-max-5x"}
	notes := RecentPlanFollows(bound, now.Add(24*time.Hour))
	if len(notes) != 1 || !strings.Contains(notes[0], "Claude Code") ||
		!strings.Contains(notes[0], "`tokenops plan set anthropic claude-max-20x`") {
		t.Fatalf("notes = %q", notes)
	}
	if got := RecentPlanFollows(bound, now.Add(8*24*time.Hour)); len(got) != 0 {
		t.Errorf("a week later still noted: %q", got)
	}
	if got := RecentPlanFollows(map[string]string{"anthropic": "claude-max-20x"}, now.Add(time.Hour)); len(got) != 0 {
		t.Errorf("noted after the operator's correction: %q", got)
	}
}

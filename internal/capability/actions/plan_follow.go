package actions

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/planevidence"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// ClientPlan is a provider's plan as its client reports it, set against
// the binding: one TokenOps followed, or one it kept because the operator
// chose otherwise after the report.
type ClientPlan struct {
	Provider string `json:"provider"`
	// Plan is the binding now: the reported plan when followed, the
	// operator's when kept.
	Plan string `json:"plan"`
	// Previous is the binding the follow replaced; empty when none.
	Previous string `json:"previous,omitempty"`
	// Reported is the plan the client reports.
	Reported string `json:"reported"`
	// Client is the reporting client by name (Claude Code, Codex), and
	// Detail what it reported.
	Client string `json:"client"`
	Detail string `json:"detail"`
	// Since is when the client first reported Reported, which is when a
	// followed plan takes effect.
	Since time.Time `json:"since"`
}

// Note is the one plain line an operator reads about a followed plan,
// with the command that undoes it.
func (c ClientPlan) Note() string {
	if c.Plan != c.Reported {
		return fmt.Sprintf("%s stays on %s, as you set it after %s started reporting %s (%s)",
			c.Provider, c.Plan, c.Client, c.Reported, c.Detail)
	}
	undo := "`tokenops plan set " + c.Provider + " <plan>`"
	was := ""
	if c.Previous != "" {
		undo = "`tokenops plan set " + c.Provider + " " + c.Previous + "`"
		was = ", was " + c.Previous
	}
	return fmt.Sprintf("%s now on %s from %s, as %s reports (%s%s) — %s if that is wrong",
		c.Provider, c.Plan, c.Since.UTC().Format("2006-01-02 15:04 MST"), c.Client, c.Detail, was, undo)
}

// FollowResult is what one pass over the clients' reports did.
type FollowResult struct {
	Followed []ClientPlan `json:"followed,omitempty"`
	Kept     []ClientPlan `json:"kept,omitempty"`
}

// FollowClientPlans binds each provider to the plan its client reports
// (Claude Code's account tier, Codex's plan_type): autonomous by default,
// the operator corrects after. Only a report naming exactly one plan
// counts; an ambiguous or missing one changes nothing.
//
// A followed plan takes effect from when the client first reported it,
// and is recorded in the plan history and the audit log with the
// client as actor and its report as evidence. An operator's own change
// made after the client started reporting its plan wins until the client
// reports a different plan, so TokenOps never fights a correction.
func FollowClientPlans(ctx context.Context, path string, evidence []planevidence.Evidence, store *sqlite.Store, now time.Time) (FollowResult, error) {
	var out FollowResult
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return out, inputErr(err)
	}
	reports, err := planhistory.DefaultReports()
	if err != nil {
		return out, err
	}
	seen, err := reports.Load()
	if err != nil {
		return out, err
	}
	history, err := loadHistory()
	if err != nil {
		return out, err
	}
	dirty := false
	for _, e := range evidence {
		if e.Plan == "" {
			continue
		}
		r, ok := seen[e.Provider]
		if !ok || r.Plan != e.Plan {
			r = planhistory.Report{Plan: e.Plan, Client: e.From, Detail: e.Detail, FirstSeen: now}
			seen[e.Provider], dirty = r, true
		}
		bound := cfg.Plans[e.Provider]
		if bound == e.Plan {
			continue
		}
		c := ClientPlan{Provider: e.Provider, Plan: e.Plan, Previous: bound, Reported: e.Plan,
			Client: e.From, Detail: e.Detail, Since: r.FirstSeen}
		if choseOtherwise(history, e.Provider, e.Plan, r.FirstSeen) {
			c.Plan, c.Previous = bound, ""
			out.Kept = append(out.Kept, c)
			continue
		}
		if err := follow(ctx, path, e, store, r.FirstSeen, now); err != nil {
			return out, err
		}
		r.FollowedFrom, r.FollowedAt = bound, now
		seen[e.Provider], dirty = r, true
		out.Followed = append(out.Followed, c)
	}
	if dirty {
		if err := reports.Save(seen); err != nil {
			return out, err
		}
	}
	return out, nil
}

// planFollowNoted is how long the glance notes a plan TokenOps followed.
const planFollowNoted = 7 * 24 * time.Hour

// RecentPlanFollows is one note per plan TokenOps bound on a client's
// word in the last week that is still bound, with the command that
// undoes it. An unreadable record notes nothing.
func RecentPlanFollows(bound map[string]string, now time.Time) []string {
	reports, err := planhistory.DefaultReports()
	if err != nil {
		return nil
	}
	seen, err := reports.Load()
	if err != nil {
		return nil
	}
	var out []string
	for _, provider := range slices.Sorted(maps.Keys(seen)) {
		r := seen[provider]
		if r.FollowedAt.IsZero() || bound[provider] != r.Plan || r.FollowedAt.After(now) || now.Sub(r.FollowedAt) > planFollowNoted {
			continue
		}
		out = append(out, ClientPlan{Provider: provider, Plan: r.Plan, Previous: r.FollowedFrom, Reported: r.Plan,
			Client: r.Client, Detail: r.Detail, Since: r.FirstSeen}.Note())
	}
	return out
}

// follow binds the reported plan from since on.
func follow(ctx context.Context, path string, e planevidence.Evidence, store *sqlite.Store, since, now time.Time) error {
	req := PlanRequest{Provider: e.Provider, Plan: e.Plan, Actor: e.Client, Store: store, Evidence: evidenceOf(e)}
	if since.Before(now) {
		req.Since = since.UTC().Format(time.RFC3339Nano)
	}
	_, err := SetPlan(ctx, path, req, now)
	return err
}

// evidenceOf is the client's report as the audit entry keeps it.
func evidenceOf(e planevidence.Evidence) map[string]any {
	out := map[string]any{"client": e.From, "detail": e.Detail}
	for k, v := range e.Facts {
		out[k] = v
	}
	return out
}

// choseOtherwise reports whether a binding other than reported was
// recorded for provider at or after since: the operator's correction (a
// `plan set`, an unset) made once the client already said what it says.
func choseOtherwise(h plans.History, provider, reported string, since time.Time) bool {
	for _, b := range h {
		if b.Provider == provider && b.Plan != reported && !b.Recorded.Before(since) {
			return true
		}
	}
	return false
}

func loadHistory() (plans.History, error) {
	f, err := planhistory.Default()
	if err != nil {
		return nil, err
	}
	return f.Load()
}

package teamshare

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
	"go.klarlabs.de/tokenops/internal/contexts/governance/taskruntime"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	accountsapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/accounts"
	anthropicadmin "go.klarlabs.de/tokenops/internal/infra/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/pkg/eventschema"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// The sections of a schema 3 upload (ADR 0012 §11): the turns' cost by
// provider, the finished work and the value placed on it, and, from an
// owner's or admin's machine, what the vendors billed. They are built
// from what this machine already computes for its own surfaces — the
// turns, `tokenops story --runtime`'s tasks and `tokenops spend units`'
// joins — reduced to teamwire's labels and sums. No model, commit subject
// or session leaves.

// Schema3 are the readers of a schema 3 upload's sections beyond the
// turns. A nil field leaves its section out.
type Schema3 struct {
	// Placed are the commits and merged pull requests landed in
	// [since, until) with the work placed on them, read from turns when
	// it is set.
	Placed func(ctx context.Context, turns TurnReader, since, until time.Time) ([]uniteconomics.Placed, error)
	// VendorCosts are, asked only on an owner's or admin's machine, the
	// readers of what each provider's cost API reports the organisation
	// was billed per UTC day ("2006-01-02") from start to end, with the
	// admin key configured on this machine; a provider without one is
	// absent. Asked on each upload, so a key added later is used.
	VendorCosts func(ctx context.Context) map[string]VendorCostReader
}

// VendorCostReader reads a provider's billed cost per UTC day.
type VendorCostReader func(ctx context.Context, start, end time.Time) (map[string]float64, error)

// PlacedFor joins this machine's work to its commits and pull requests
// as `tokenops spend units` does.
func PlacedFor(d uniteconomics.Deps) func(ctx context.Context, turns TurnReader, since, until time.Time) ([]uniteconomics.Placed, error) {
	return func(ctx context.Context, turns TurnReader, since, until time.Time) ([]uniteconomics.Placed, error) {
		d := d
		if turns != nil {
			d.Turns = turns
		}
		in, err := uniteconomics.Gather(ctx, d, since.Add(-uniteconomics.PRMaxGap), since, until)
		if err != nil {
			return nil, err
		}
		return in.PlacedUnits(), nil
	}
}

// addSpend fills u's schema 3 sections and marks it schema 3. A reader
// that fails leaves its section empty with a warning.
func addSpend(ctx context.Context, d Deps, opts Options, shared TurnReader, u *teamwire.Upload, units []sessions.Unit,
	turns []analytics.SessionTurn, repoOf map[string]string, now time.Time) []string {
	u.Schema = teamwire.SchemaSpend
	first, err := time.Parse(teamwire.DayLayout, u.Days[0])
	if err != nil {
		return []string{"spend: " + err.Error()}
	}
	covered := map[string]bool{}
	for _, day := range u.Days {
		covered[day] = true
	}
	var warnings []string
	u.Spend = SpendRows(turns, repoOf, covered)
	var placed []uniteconomics.Placed
	if d.Schema3.Placed != nil {
		done := lap(opts.Took, "units of work")
		placed, err = d.Schema3.Placed(ctx, shared, first, now)
		done()
		if err != nil {
			warnings = append(warnings, "units: "+err.Error())
		}
	}
	labels := map[string]string{}
	labelOf := func(root string) string {
		l, ok := labels[root]
		if !ok {
			l = teamwire.RepoNone
			if d.Repos != nil {
				l = repoLabel(ctx, d.Repos, root, opts.RepoNames)
			}
			labels[root] = l
		}
		return l
	}
	u.Units = UnitRows(story.Group(units, story.Options{}), turns, placed, repoOf, labelOf, covered)
	if slices.Contains(teamwire.VendorCostRoles, opts.Role) && d.Schema3.VendorCosts != nil {
		var w []string
		done := lap(opts.Took, "vendor cost")
		u.VendorCosts, w = vendorCostRows(ctx, d.Schema3.VendorCosts(ctx), u.Days, first, now)
		done()
		warnings = append(warnings, w...)
	}
	return append(warnings, dropInvalidSpend(u)...)
}

// SpendRows sums the covered days' turns by day, repository and provider.
func SpendRows(turns []analytics.SessionTurn, repoOf map[string]string, covered map[string]bool) []teamwire.SpendBucket {
	type key struct{ day, repo, provider string }
	rows := map[key]*teamwire.SpendBucket{}
	for _, t := range turns {
		day := t.At.UTC().Format(teamwire.DayLayout)
		if !covered[day] {
			continue
		}
		k := key{day, repoOr(repoOf[t.SessionID]), t.Provider}
		r := rows[k]
		if r == nil {
			r = &teamwire.SpendBucket{Day: k.day, Repo: k.repo, Provider: k.provider}
			rows[k] = r
		}
		r.Turns++
		r.Tokens += t.Tokens
		if t.Priced {
			// Billed as the FOCUS export bills it: metered usage stored
			// without a cost at its list price.
			r.CostUSD += t.BilledUSD()
			r.APIEquivalentUSD += t.APIEquivalentUSD
		} else {
			r.UnpricedTurns++
		}
	}
	out := make([]teamwire.SpendBucket, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Provider < b.Provider
	})
	return out
}

// UnitRows files the covered days' finished work by day and repository:
// each task with recorded usage under the day it started and its
// session's repository, each placed commit and pull request under the
// day it landed and labelOf its repository.
func UnitRows(tasks []story.Task, turns []analytics.SessionTurn, placed []uniteconomics.Placed,
	repoOf map[string]string, labelOf func(root string) string, covered map[string]bool) []teamwire.UnitBucket {
	type key struct{ day, repo string }
	rows := map[key]*teamwire.UnitBucket{}
	get := func(day, repo string) *teamwire.UnitBucket {
		k := key{day, repoOr(repo)}
		r := rows[k]
		if r == nil {
			r = &teamwire.UnitBucket{Day: k.day, Repo: k.repo}
			rows[k] = r
		}
		return r
	}
	recorded := make([]taskruntime.Turn, 0, len(turns))
	for _, t := range turns {
		recorded = append(recorded, taskruntime.Turn{SessionID: t.SessionID, At: t.At,
			BilledUSD: t.BilledUSD(), APIEquivalentUSD: t.APIEquivalentUSD, Priced: t.Priced})
	}
	usage, _ := taskruntime.PlaceTurns(tasks, recorded)
	for i, t := range tasks {
		day := t.Start.UTC().Format(teamwire.DayLayout)
		if usage[i] == nil || !covered[day] {
			continue
		}
		r := get(day, repoOf[t.SessionID])
		r.Tasks++
		r.TaskUSD += usage[i].APIEquivalentUSD
	}
	for _, p := range placed {
		day := p.At.UTC().Format(teamwire.DayLayout)
		if !covered[day] {
			continue
		}
		r := get(day, labelOf(p.Root))
		switch p.Unit {
		case uniteconomics.UnitCommit:
			r.Commits++
			r.CommitUSD += p.APIEquivalentUSD
		case uniteconomics.UnitMergedPR:
			r.MergedPRs++
			r.MergedPRUSD += p.APIEquivalentUSD
		}
	}
	out := make([]teamwire.UnitBucket, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

// vendorCostRows reads each provider's daily cost over the covered days.
// A day the vendor has not reported yet (today, usually) is left out
// rather than sent as 0.
func vendorCostRows(ctx context.Context, readers map[string]VendorCostReader, days []string, first, now time.Time) ([]teamwire.VendorCost, []string) {
	providers := make([]string, 0, len(readers))
	for p := range readers {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	today := now.UTC().Format(teamwire.DayLayout)
	end := first.AddDate(0, 0, len(days))
	var out []teamwire.VendorCost
	var warnings []string
	for _, p := range providers {
		got, err := readers[p](ctx, first, end)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("vendor cost (%s): %v", p, err))
			continue
		}
		for _, day := range days {
			if v, ok := got[day]; ok && day != today {
				out = append(out, teamwire.VendorCost{Day: day, Provider: p, CostUSD: v})
			}
		}
	}
	return out, warnings
}

func repoOr(repo string) string {
	if repo == "" {
		return teamwire.RepoNone
	}
	return repo
}

// dropInvalidSpend removes the schema 3 rows that would fail validation,
// saying how many.
func dropInvalidSpend(u *teamwire.Upload) []string {
	var warnings []string
	note := func(section string, n int) {
		if n > 0 {
			warnings = append(warnings, fmt.Sprintf("%s: %d rows failed validation and were left out", section, n))
		}
	}
	var n int
	u.Spend, n = keepValid(u.Spend, teamwire.SpendBucket.Validate)
	note("spend", n)
	u.Units, n = keepValid(u.Units, teamwire.UnitBucket.Validate)
	note("units", n)
	u.VendorCosts, n = keepValid(u.VendorCosts, teamwire.VendorCost.Validate)
	note("vendor cost", n)
	return warnings
}

// VendorCostsFor reads each provider's cost API with the admin key keys
// finds for it; a provider without a key, or without a cost API, is
// absent.
func VendorCostsFor(keysOf func(context.Context) map[string]string) func(context.Context) map[string]VendorCostReader {
	return func(ctx context.Context) map[string]VendorCostReader { return vendorCostReaders(keysOf(ctx)) }
}

func vendorCostReaders(keys map[string]string) map[string]VendorCostReader {
	out := map[string]VendorCostReader{}
	if k := keys[string(eventschema.ProviderAnthropic)]; k != "" {
		c := anthropicadmin.NewAdminClient(k)
		out[string(eventschema.ProviderAnthropic)] = c.DailyCost
	}
	if k := keys[string(eventschema.ProviderOpenAI)]; k != "" {
		out[string(eventschema.ProviderOpenAI)] = func(ctx context.Context, start, end time.Time) (map[string]float64, error) {
			return accountsapi.OpenAIAdmin{}.DailyCost(ctx, k, start, end)
		}
	}
	return out
}

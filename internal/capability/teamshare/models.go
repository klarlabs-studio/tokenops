package teamshare

import (
	"cmp"
	"fmt"
	"slices"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Schema 6's spend by model is the spend rows' turns split further by the
// model that served them, as its public catalog ID (teamwire.CatalogModel,
// the row the rate card prices it by; anything else is "other"), and by
// the coding agent that recorded them (the harness allocation files a
// turn's source under; any other source is "other"). Reasoning effort is
// not split: the client records it per instruction, as the client's own
// free-form label, not per turn.

// addModelSpend fills u's spend by model from the same turns as its spend
// rows. A split that would not cross whole — more rows than the bound, or
// a row that fails validation — is left out and noted, since a partial
// split would contradict the spend rows.
func addModelSpend(u *teamwire.Upload, turns []analytics.SessionTurn, repoOf map[string]string, notes *Notes) {
	if len(u.Spend) == 0 {
		return
	}
	covered := make(map[string]bool, len(u.Spend))
	for _, s := range u.Spend {
		covered[s.Day] = true
	}
	rows := ModelSpendRows(turns, repoOf, covered)
	sent := make(map[string]bool, len(u.Spend))
	for _, s := range u.Spend {
		sent[s.Day+"/"+s.Repo+"/"+s.Provider] = true
	}
	rows = slices.DeleteFunc(rows, func(r teamwire.ModelSpend) bool { return !sent[r.Day+"/"+r.Repo+"/"+r.Provider] })
	if len(rows) > teamwire.MaxModelSpend {
		notes.Dropped = append(notes.Dropped, fmt.Sprintf("spend by model: %d rows, more than the %d an upload carries; left out", len(rows), teamwire.MaxModelSpend))
		return
	}
	for _, r := range rows {
		if err := r.Validate(); err != nil {
			notes.Dropped = append(notes.Dropped, "spend by model: a row failed validation, so the split was left out: "+err.Error())
			return
		}
	}
	u.SpendModels = rows
}

// ModelSpendRows sums the covered days' turns by day, repository,
// provider, catalog model and agent.
func ModelSpendRows(turns []analytics.SessionTurn, repoOf map[string]string, covered map[string]bool) []teamwire.ModelSpend {
	type key struct{ day, repo, provider, model, agent string }
	type modelKey struct{ provider, model string }
	ids := map[modelKey]string{}
	rows := map[key]*teamwire.ModelSpend{}
	for _, t := range turns {
		day := t.At.UTC().Format(teamwire.DayLayout)
		if !covered[day] {
			continue
		}
		mk := modelKey{t.Provider, t.Model}
		id, ok := ids[mk]
		if !ok {
			id = teamwire.CatalogModel(t.Provider, t.Model)
			ids[mk] = id
		}
		k := key{day, repoOr(repoOf[t.SessionID]), t.Provider, id, AgentOf(t.Source)}
		r := rows[k]
		if r == nil {
			r = &teamwire.ModelSpend{Day: k.day, Repo: k.repo, Provider: k.provider, Model: k.model, Agent: k.agent}
			rows[k] = r
		}
		r.Turns++
		r.Tokens += t.Tokens
		if t.Priced {
			r.CostUSD += t.BilledUSD()
			r.APIEquivalentUSD += t.APIEquivalentUSD
		} else {
			r.UnpricedTurns++
		}
	}
	out := make([]teamwire.ModelSpend, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b teamwire.ModelSpend) int {
		return cmp.Or(cmp.Compare(a.Day, b.Day), cmp.Compare(a.Repo, b.Repo), cmp.Compare(a.Provider, b.Provider),
			cmp.Compare(a.Model, b.Model), cmp.Compare(a.Agent, b.Agent))
	})
	return out
}

// AgentOf is the agent a turn recorded by source is filed under: its
// harness, or teamwire.AgentOther for a source no harness is known for.
func AgentOf(source string) string {
	if h := allocation.HarnessOf(source); teamwire.ValidAgent(h) {
		return h
	}
	return teamwire.AgentOther
}

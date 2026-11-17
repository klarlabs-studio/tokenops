package teamshare

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Schema 6's spend by model is the spend rows' turns split further by the
// model that served them, as its public catalog ID (teamwire.CatalogModel,
// the row the rate card prices it by; anything else is "other"), by the
// coding agent that recorded them (the harness allocation files a turn's
// source under; any other source is "other"), and by reasoning effort.
// Clients record effort with the instruction (Claude Code on each entry
// of the session, Codex on each turn's context, opencode on each message),
// and the transcripts are read per instruction: a turn takes the effort
// that served most of the instruction it answered (agentdx.Unit.Effort),
// as the client's label mapped to the closed set (teamwire.EffortLevel).
// A turn that answered no instruction the transcripts hold — the proxy,
// Cursor, a client that records no effort — is "unknown".

// addModelSpend fills u's spend by model from the same turns as its spend
// rows. A split by effort that would pass the row bound is sent with
// every row's effort as "other", the split by model and agent alone; a
// split that still would not cross whole — more rows than the bound, or a
// row that fails validation — is left out and noted, since a partial
// split would contradict the spend rows.
func addModelSpend(u *teamwire.Upload, turns []analytics.SessionTurn, units []sessions.Unit, repoOf map[string]string, notes *Notes) {
	if len(u.Spend) == 0 {
		return
	}
	covered := make(map[string]bool, len(u.Spend))
	sent := make(map[string]bool, len(u.Spend))
	for _, s := range u.Spend {
		covered[s.Day] = true
		sent[s.Day+"/"+s.Repo+"/"+s.Provider] = true
	}
	split := func(byEffort bool) []teamwire.ModelSpend {
		rows := ModelSpendRows(turns, units, repoOf, covered, byEffort)
		return slices.DeleteFunc(rows, func(r teamwire.ModelSpend) bool { return !sent[r.Day+"/"+r.Repo+"/"+r.Provider] })
	}
	rows := split(true)
	if len(rows) > teamwire.MaxModelSpend {
		n := len(rows)
		rows = split(false)
		if len(rows) > teamwire.MaxModelSpend {
			notes.Dropped = append(notes.Dropped, fmt.Sprintf("spend by model: %d rows, more than the %d an upload carries; left out",
				len(rows), teamwire.MaxModelSpend))
			return
		}
		notes.Dropped = append(notes.Dropped, fmt.Sprintf("spend by model: %d rows split by effort, more than the %d an upload carries; "+
			"effort sent as %q", n, teamwire.MaxModelSpend, teamwire.EffortOther))
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
// provider, catalog model, agent and, with byEffort, the effort of the
// instruction each answered (turnEffort); without it every row's effort is
// teamwire.EffortOther.
func ModelSpendRows(turns []analytics.SessionTurn, units []sessions.Unit, repoOf map[string]string, covered map[string]bool,
	byEffort bool) []teamwire.ModelSpend {
	type key struct{ day, repo, provider, model, agent, effort string }
	type modelKey struct{ provider, model string }
	effortOf := func(string, time.Time) string { return teamwire.EffortOther }
	if byEffort {
		effortOf = turnEffort(units)
	}
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
		k := key{day, repoOr(repoOf[t.SessionID]), t.Provider, id, AgentOf(t.Source), effortOf(t.SessionID, t.At)}
		r := rows[k]
		if r == nil {
			r = &teamwire.ModelSpend{Day: k.day, Repo: k.repo, Provider: k.provider, Model: k.model, Agent: k.agent, Effort: k.effort}
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
			cmp.Compare(a.Model, b.Model), cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Effort, b.Effort))
	})
	return out
}

// turnEffort is the effort a session's turn at a moment crosses as: that
// of the session's instruction whose span holds the moment, from its
// prompt to its last activity, mapped by teamwire.EffortLevel; unknown
// outside every instruction.
func turnEffort(units []sessions.Unit) func(session string, at time.Time) string {
	type span struct {
		start, end time.Time
		effort     string
	}
	bySession := map[string][]span{}
	for _, u := range units {
		end := u.End
		if u.LastActivity.After(end) {
			end = u.LastActivity
		}
		bySession[u.SessionID] = append(bySession[u.SessionID], span{u.Start, end, teamwire.EffortLevel(u.Effort)})
	}
	for _, ss := range bySession {
		slices.SortFunc(ss, func(a, b span) int { return a.start.Compare(b.start) })
	}
	return func(session string, at time.Time) string {
		ss := bySession[session]
		// The last instruction typed at or before the moment.
		i := sort.Search(len(ss), func(i int) bool { return ss[i].start.After(at) }) - 1
		if i < 0 || at.After(ss[i].end) {
			return teamwire.EffortUnknown
		}
		return ss[i].effort
	}
}

// AgentOf is the agent a turn recorded by source is filed under: its
// harness, or teamwire.AgentOther for a source no harness is known for.
func AgentOf(source string) string {
	if h := allocation.HarnessOf(source); teamwire.ValidAgent(h) {
		return h
	}
	return teamwire.AgentOther
}

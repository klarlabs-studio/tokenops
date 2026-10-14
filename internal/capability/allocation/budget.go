package allocation

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/budgets"
	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

// ErrNoRules is returned when a tag is asked for and no rule could give it.
var ErrNoRules = errors.New("no allocation rules are configured")

// TagMeter measures the work carrying one tag, for a budget scoped to it.
type TagMeter struct {
	Turns commits.TurnReader
	Rules []Rule
	// Dirs maps sessions to where they ran; nil reads the clients'
	// defaults.
	Dirs func(since time.Time) map[string]sessiondirs.Dir

	once  sync.Once
	repos *gitlog.Repos
}

// SummarizeTag sums the turns in f whose session carries tag ("key=value"):
// tokens, what was billed (nothing where a plan covered it) and the API
// equivalent.
func (m *TagMeter) SummarizeTag(ctx context.Context, f analytics.Filter, tag string) (analytics.Summary, error) {
	scope, err := ParseScope(tag)
	if err != nil {
		return analytics.Summary{}, err
	}
	if len(m.Rules) == 0 {
		return analytics.Summary{}, ErrNoRules
	}
	m.once.Do(func() { m.repos = gitlog.NewRepos() })
	turns, err := Tagged(ctx, Deps{Turns: commits.TurnsIn(m.Turns, f), Dirs: m.Dirs, Repos: m.repos, Rules: m.Rules}, f.Since, f.Until)
	if err != nil {
		return analytics.Summary{}, err
	}
	var s analytics.Summary
	for _, t := range turns {
		if !scope.Holds(t.Tags) {
			continue
		}
		s.Requests++
		s.InputTokens += t.InputTokens
		s.OutputTokens += t.OutputTokens
		s.TotalTokens += t.Tokens
		s.APIEquivalentUSD += t.APIEquivalentUSD
		s.CostUSD += t.BilledUSD()
	}
	return s, nil
}

// budgetSource is a budget source that also measures tags.
type budgetSource struct {
	budgets.Source
	*TagMeter
}

// BudgetSource is src, able to measure a budget scoped to a tag as well.
func BudgetSource(src budgets.Source, m *TagMeter) budgets.Source {
	return budgetSource{Source: src, TagMeter: m}
}

package boundaries

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// LoopBreaker summarises the config's loop breaker as PauseSummary does
// the headroom pause: the default rule when it is there, else the
// operator's first continue rule on loop_calls, and the sessions it paused,
// or in shadow would have paused, over the last week. installed is
// Installed's answer; only Claude Code's hook sends the input a loop is
// read from, so only it is a pause point here.
func LoopBreaker(cfg config.Config, env Env, installed map[string]bool) (PauseSummary, error) {
	r, ok := loopBreakerRule(cfg.BoundaryRules())
	if !ok {
		return PauseSummary{}, nil
	}
	out := PauseSummary{
		Configured: true, Rule: r.Name, Stage: string(r.Stage),
		PausePoint: installed[HarnessClaudeCode],
		When:       loopWords(r), Require: string(r.Require.Kind),
	}
	sessions, err := env.Dir.Sessions()
	if err != nil {
		return PauseSummary{}, err
	}
	since := env.now().Add(-pauseWeek)
	for _, s := range sessions {
		if s.Matches[r.Name] > 0 && !s.LastAt.Before(since) {
			out.Sessions++
		}
	}
	return out, nil
}

func loopBreakerRule(rules []domain.Rule) (domain.Rule, bool) {
	var found *domain.Rule
	for i, r := range rules {
		if r.Action != domain.ActionContinue || !readsLoop(r) {
			continue
		}
		if r.Name == config.LoopBreakerName {
			return r, true
		}
		if found == nil {
			found = &rules[i]
		}
	}
	if found == nil {
		return domain.Rule{}, false
	}
	return *found, true
}

// loopWords words a rule's loop_calls condition: "the agent's calls go
// round a loop for 4 calls or more with no file changed".
func loopWords(r domain.Rule) string {
	for _, c := range r.When {
		if c.Field != domain.FieldLoopCalls {
			continue
		}
		n := fmt.Sprintf("%g calls or more", c.Number)
		if c.Op == domain.OpGT {
			n = fmt.Sprintf("more than %g calls", c.Number)
		}
		return "the agent's calls go round a loop for " + n + " with no file changed"
	}
	return ""
}

// AddLoopBreaker writes the default loop breaker, in shadow, into the
// config at path, unless a rule there already reads loop_calls. Like
// AddHeadroomPause, it is asked for, never added behind the operator's
// back.
func AddLoopBreaker(path string) (PauseAdded, error) {
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return PauseAdded{}, actions.InputError{Err: err}
	}
	if r, ok := loopBreakerRule(cfg.BoundaryRules()); ok {
		return PauseAdded{Rule: r.Name, Stage: string(r.Stage), Config: path,
			Note: "a loop breaker is already set; `tokenops boundaries status` shows what it did"}, nil
	}
	cfg.Boundaries = append(cfg.Boundaries, config.LoopBreakerRule())
	if err := cfg.Validate(); err != nil {
		return PauseAdded{}, actions.InputError{Err: err}
	}
	if err := config.WriteMutable(path, cfg); err != nil {
		return PauseAdded{}, err
	}
	return PauseAdded{Added: true, Rule: config.LoopBreakerName, Stage: string(domain.StageShadow), Config: path,
		Note: "in shadow at 4 calls round a loop with no file changed: it records each session it would have paused and binds nothing; " +
			"`tokenops boundaries promote " + config.LoopBreakerName + "` makes it pause"}, nil
}

package boundaries

import (
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// pauseWeek is how far back the headroom pause's sessions are counted.
const pauseWeek = 7 * 24 * time.Hour

// PauseSummary is the headroom pause as the operator sees it: whether one
// is set, its stage, and the sessions it paused, or in shadow would have
// paused, over the last week.
type PauseSummary struct {
	Configured bool   `json:"configured"`
	Rule       string `json:"rule,omitempty"`
	Stage      string `json:"stage,omitempty"`
	// Sessions are the sessions the rule matched in over the last week:
	// a pause asks once per session, so this is how many it paused.
	Sessions int `json:"sessions"`
	// PausePoint is whether a harness that can pause has the boundary
	// hook. Without one the rule records nothing.
	PausePoint bool `json:"pause_point"`
}

// HeadroomPause summarises the config's headroom pause: the default rule
// when it is there, else the operator's first continue rule on plan
// headroom. installed is Installed's answer.
func HeadroomPause(cfg config.Config, env Env, installed map[string]bool) (PauseSummary, error) {
	r, ok := headroomPauseRule(cfg.BoundaryRules())
	if !ok {
		return PauseSummary{}, nil
	}
	out := PauseSummary{
		Configured: true, Rule: r.Name, Stage: string(r.Stage),
		PausePoint: installed[HarnessClaudeCode] || installed[HarnessOpencode],
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

func headroomPauseRule(rules []domain.Rule) (domain.Rule, bool) {
	var found *domain.Rule
	for i, r := range rules {
		if r.Action != domain.ActionContinue || !pausesOnHeadroom(r) {
			continue
		}
		if r.Name == config.HeadroomPauseName {
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

func pausesOnHeadroom(r domain.Rule) bool {
	for _, c := range r.When {
		if c.Field == domain.FieldPlanLeft {
			return true
		}
	}
	return false
}

// NeedsPausePoint reports whether any rule is decided at the pause point
// before a tool call — continue and spend rules — so the boundary hook
// has something to evaluate.
func NeedsPausePoint(rules []domain.Rule) bool { return needsHook(rules) }

// PauseAdded is what AddHeadroomPause did.
type PauseAdded struct {
	// Added is false when the config already paused on headroom; Rule is
	// then the rule that does.
	Added  bool   `json:"added"`
	Rule   string `json:"rule"`
	Stage  string `json:"stage"`
	Config string `json:"config"`
	Note   string `json:"note"`
}

// AddHeadroomPause writes the default headroom pause, in shadow, into the
// config at path, unless a rule there already pauses on plan headroom. It
// is how an install made before init wrote the pause gets one: asked for,
// never added behind the operator's back.
func AddHeadroomPause(path string) (PauseAdded, error) {
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return PauseAdded{}, actions.InputError{Err: err}
	}
	if r, ok := headroomPauseRule(cfg.BoundaryRules()); ok {
		return PauseAdded{Rule: r.Name, Stage: string(r.Stage), Config: path,
			Note: "a headroom pause is already set; `tokenops boundaries status` shows what it did"}, nil
	}
	cfg.Boundaries = append(cfg.Boundaries, config.HeadroomPauseRule())
	if err := cfg.Validate(); err != nil {
		return PauseAdded{}, actions.InputError{Err: err}
	}
	if err := config.WriteMutable(path, cfg); err != nil {
		return PauseAdded{}, err
	}
	return PauseAdded{Added: true, Rule: config.HeadroomPauseName, Stage: string(domain.StageShadow), Config: path,
		Note: "in shadow at 90% of the tightest plan window: it records each session it would have paused and binds nothing; " +
			"`tokenops boundaries promote " + config.HeadroomPauseName + "` makes it pause"}, nil
}

package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/claudehome"
	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/opencodeplugin"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
)

// HookVerb is the command every refusing hook runs; it is also the marker
// the installers and this status find the hook by.
const HookVerb = "boundary-guard"

// claudeSubagentHook keys Installed's answer for route-guard's Agent
// hook, which evaluates subagent_model rules in Claude Code.
const claudeSubagentHook = "claude-code/subagent"

// Rows for the decision points inside TokenOps, keyed in Installed's
// answer by whether each is wired.
const (
	rowRouter    = "router"
	rowShareTeam = "team upload"
	rowShareOTel = "otel export"
)

// Wired is Installed's answer for the default client paths, plus whether
// each decision point inside TokenOps is wired in cfg: the router (routing
// configured), the team upload (this machine joined a team) and the OTel
// export (configured).
func Wired(cfg config.Config) map[string]bool {
	out := Installed("", "")
	out[rowRouter] = cfg.RouterConfig() != nil
	if cfg.Team.UploadsEnabled() {
		if path, err := teamclient.ResolveStatePath(cfg.Team.StatePath); err == nil {
			_, err := teamclient.Load(path)
			out[rowShareTeam] = err == nil
		}
	}
	out[rowShareOTel] = cfg.OTel.MetricsEnabled() || cfg.OTel.EventsEnabled()
	return out
}

// Enforcement is what a rule does in one harness.
const (
	EnforcementEnforced     = "enforced"
	EnforcementShadow       = "shadow"
	EnforcementAdviceOnly   = "advice only"
	EnforcementNotInstalled = "hook not installed"
	// EnforcementIdle is a decision point inside TokenOps that is not in
	// use, so the rule has nothing to decide there.
	EnforcementIdle = "idle"
	// EnforcementNotEvaluated is a harness that does not give the figure
	// the rule reads, so the rule never matches there.
	EnforcementNotEvaluated = "not evaluated"
)

// HarnessStatus is how one harness treats a rule.
type HarnessStatus struct {
	Harness     string `json:"harness"`
	Enforcement string `json:"enforcement"`
	Note        string `json:"note,omitempty"`
}

// RuleStatus is one rule, how often it matched, and where it binds.
type RuleStatus struct {
	Name    string `json:"name"`
	Action  string `json:"action"`
	Require string `json:"require"`
	Stage   string `json:"stage"`
	// Matches counts the calls the rule matched; Sessions, the sessions
	// it matched in. For a shadow rule these are how often it would have
	// fired.
	Matches     int             `json:"matches"`
	Sessions    int             `json:"sessions"`
	LastMatched *time.Time      `json:"last_matched,omitempty"`
	Harnesses   []HarnessStatus `json:"harnesses"`
	Note        string          `json:"note,omitempty"`
	// Source is local (config.yaml) or organisation (the team plane).
	Source string `json:"source"`
	// When are the rule's conditions; all must hold for it to apply.
	When []ConditionStatus `json:"when,omitempty"`
}

// ConditionStatus is one condition of a rule, as config.yaml reads, and
// whether it holds now where that can be said without a session: the time
// of day can, the session's folder or cost cannot.
type ConditionStatus struct {
	Condition string `json:"condition"`
	// HoldsNow is set for hours conditions only.
	HoldsNow *bool `json:"holds_now,omitempty"`
}

// PendingView is a decision waiting on an answer.
type PendingView struct {
	ID        string    `json:"id"`
	Rule      string    `json:"rule"`
	Require   string    `json:"require"`
	Crossed   string    `json:"crossed"`
	Harness   string    `json:"harness"`
	SessionID string    `json:"session_id"`
	At        time.Time `json:"at"`
	Approve   string    `json:"approve"`
	// Routed is a decision the organisation answers, not this machine.
	Routed bool `json:"routed,omitempty"`
}

// Status is every rule and every decision waiting on the operator.
type Status struct {
	Rules   []RuleStatus  `json:"rules"`
	Pending []PendingView `json:"pending"`
	Note    string        `json:"note,omitempty"`
	// Presence is whether approvals on this machine can be proven to come
	// from a person present (ADR 0015), and what the shadow check found.
	// Absent when no rule is configured, since nothing asks for approval.
	Presence *PresenceStatus `json:"presence,omitempty"`
}

// Installed reports, per harness that can refuse, whether its boundary
// hook is wired: Claude Code's settings.json and opencode's generated
// plugin. Empty paths take the defaults.
func Installed(claudeSettings, opencodePluginDir string) map[string]bool {
	home, _ := os.UserHomeDir()
	if claudeSettings == "" {
		claudeSettings = claudehome.Join("settings.json")
	}
	if opencodePluginDir == "" {
		opencodePluginDir = filepath.Join(home, ".config", "opencode", "plugins")
	}
	out := map[string]bool{}
	if b, err := os.ReadFile(claudeSettings); err == nil { //nolint:gosec // the operator's own client settings
		out[HarnessClaudeCode] = strings.Contains(string(b), `"`+HookVerb+`"`)
		// subagent_model rules are evaluated by route-guard's Agent hook.
		out[claudeSubagentHook] = strings.Contains(string(b), `"route-guard"`) && strings.Contains(string(b), `"Agent"`)
	}
	if b, err := os.ReadFile(filepath.Join(opencodePluginDir, opencodeplugin.FileName)); err == nil { //nolint:gosec // TokenOps' own plugin
		out[HarnessOpencode] = opencodeplugin.Has(string(b), HookVerb)
	}
	return out
}

// RuleStatuses reports each rule: its stage, how often it matched, and,
// per harness, whether it is enforced, in shadow, or advice only.
// installed is Installed's answer.
func RuleStatuses(rules []domain.Rule, env Env, installed map[string]bool) (Status, error) {
	sessions, err := env.Dir.Sessions()
	if err != nil {
		return Status{}, err
	}
	out := Status{Rules: make([]RuleStatus, 0, len(rules)), Pending: []PendingView{}}
	for _, r := range rules {
		rs := RuleStatus{
			Name: r.Name, Action: string(r.Action), Require: r.Require.String(), Stage: string(r.Stage),
			Harnesses: harnesses(r, installed), Source: sourceName(r.Source), When: conditionStatuses(r, env),
		}
		for _, s := range sessions {
			n := s.Matches[r.Name]
			if n == 0 {
				continue
			}
			rs.Matches += n
			rs.Sessions++
			if rs.LastMatched == nil || s.LastAt.After(*rs.LastMatched) {
				at := s.LastAt
				rs.LastMatched = &at
			}
		}
		rs.Note = ruleNote(r, rs)
		out.Rules = append(out.Rules, rs)
	}
	pending, err := env.Dir.ListPending()
	if err != nil {
		return Status{}, err
	}
	now := env.now()
	for _, p := range pending {
		if _, answered, _ := env.Dir.GetAnswer(p.ID, now); answered || now.Sub(p.At) > 24*time.Hour {
			continue
		}
		out.Pending = append(out.Pending, pendingView(p))
	}
	if len(rules) > 0 {
		ps := Presence(env)
		out.Presence = &ps
	}
	switch {
	case len(rules) == 0:
		out.Note = "no boundaries configured; add a boundaries: section to config.yaml"
	case needsHook(rules) && !installed[HarnessClaudeCode] && !installed[HarnessOpencode]:
		out.Note = "no boundary hook is installed, so continue and spend rules are not evaluated: run `tokenops hooks install --boundary`"
	}
	return out, nil
}

func harnesses(r domain.Rule, installed map[string]bool) []HarnessStatus {
	switch r.Action {
	case domain.ActionModelRoute:
		return []HarnessStatus{point(r, rowRouter, installed,
			"routing is not configured, so no request is routed and the rule has nothing to hold",
			"requests through the TokenOps proxy; a held route leaves the request on its model")}
	case domain.ActionShare:
		var out []HarnessStatus
		for _, row := range []struct{ name, dest, off string }{
			{rowShareTeam, domain.DestinationTeam, "this machine has not joined a team, so nothing is uploaded"},
			{rowShareOTel, domain.DestinationOTel, "no OTel collector is configured, so nothing is exported"},
		} {
			if !shareConcerns(r, row.dest) {
				continue
			}
			out = append(out, point(r, row.name, installed, row.off, "held figures are not sent"))
		}
		return out
	}
	out := make([]HarnessStatus, 0, 4)
	for _, h := range []string{HarnessClaudeCode, HarnessOpencode} {
		hs := HarnessStatus{Harness: h}
		switch {
		case readsLoop(r) && h == HarnessOpencode:
			hs.Enforcement = EnforcementNotEvaluated
			hs.Note = loopOnlyClaudeCode
		case r.Action == domain.ActionSubagentModel && h == HarnessOpencode:
			hs.Enforcement = EnforcementAdviceOnly
			hs.Note = "opencode's subagents are not routed through a hook TokenOps can answer"
		case !installed[hookKey(r, h)]:
			hs.Enforcement = EnforcementNotInstalled
			hs.Note = "tokenops hooks install --boundary --client " + h
			if r.Action == domain.ActionSubagentModel {
				hs.Note = "tokenops hooks install --route-guard --client " + h
			}
		case r.Enforced():
			hs.Enforcement = EnforcementEnforced
		default:
			hs.Enforcement = EnforcementShadow
			hs.Note = "records what it would have done; promote to enforce"
		}
		out = append(out, hs)
	}
	for _, h := range []string{HarnessCodex, HarnessCursor} {
		if readsLoop(r) {
			out = append(out, HarnessStatus{Harness: h, Enforcement: EnforcementNotEvaluated, Note: loopOnlyClaudeCode})
			continue
		}
		if reads(r, domain.FieldFolder) {
			out = append(out, HarnessStatus{Harness: h, Enforcement: EnforcementNotEvaluated, Note: folderOnlyHooks})
			continue
		}
		out = append(out, HarnessStatus{
			Harness: h, Enforcement: EnforcementAdviceOnly,
			Note: "its hooks cannot refuse a call; the rule reaches the agent as advice through tokenops_glance",
		})
	}
	return out
}

// loopOnlyClaudeCode says why a loop rule is not evaluated in a harness.
const loopOnlyClaudeCode = "loop_calls needs each tool call's input, which only Claude Code's hook sends"

// folderOnlyHooks says why a folder rule is not evaluated in a harness.
const folderOnlyHooks = "folder needs the session's working directory, which only Claude Code's and opencode's hooks send"

// readsLoop reports whether a rule reads the loop figure.
func readsLoop(r domain.Rule) bool { return reads(r, domain.FieldLoopCalls) }

// reads reports whether a rule has a condition on f.
func reads(r domain.Rule, f domain.Field) bool {
	for _, c := range r.When {
		if c.Field == f {
			return true
		}
	}
	return false
}

// conditionStatuses lists the rule's conditions, saying for hours whether
// they hold now.
func conditionStatuses(r domain.Rule, env Env) []ConditionStatus {
	if len(r.When) == 0 {
		return nil
	}
	now := domain.Facts{Now: env.localNow()}
	out := make([]ConditionStatus, len(r.When))
	for i, c := range r.When {
		out[i] = ConditionStatus{Condition: c.String()}
		if c.Field == domain.FieldHours {
			holds := c.Holds(now)
			out[i].HoldsNow = &holds
		}
	}
	return out
}

// point is a decision point inside TokenOps: enforced or in shadow when
// wired, and otherwise idle, with why.
func point(r domain.Rule, name string, wired map[string]bool, off, on string) HarnessStatus {
	hs := HarnessStatus{Harness: name, Note: on}
	switch {
	case !wired[name]:
		hs.Enforcement, hs.Note = EnforcementIdle, off
	case r.Enforced():
		hs.Enforcement = EnforcementEnforced
	default:
		hs.Enforcement = EnforcementShadow
		hs.Note = "records what it would have done; promote to enforce"
	}
	return hs
}

// shareConcerns reports whether a share rule applies to destination: a
// rule naming none applies to both.
func shareConcerns(r domain.Rule, destination string) bool {
	for _, c := range r.When {
		if c.Field == domain.FieldDestination {
			return c.Text == destination
		}
	}
	return true
}

// needsHook reports whether any rule is decided at the pause point.
func sourceName(s domain.Source) string {
	if s == domain.SourceOrg {
		return "organisation"
	}
	return "local"
}

// pendingView is a waiting decision as status shows it, with how it is
// answered: here, or by the organisation.
func pendingView(p boundaryledger.Pending) PendingView {
	v := PendingView{
		ID: p.ID, Rule: p.Rule, Require: p.Require, Crossed: p.Crossed, Harness: p.Harness,
		SessionID: p.SessionID, At: p.At, Approve: "tokenops approve " + p.ID,
	}
	if teamRouted(p) {
		v.Approve = "answered by " + strings.TrimPrefix(p.Require, "approver:") + " in your organisation"
		if p.RoutedAt == nil {
			v.Approve += " (not sent yet: the daemon sends it when it reaches the team plane)"
		}
		v.Routed = true
	}
	return v
}

func needsHook(rules []domain.Rule) bool {
	for _, r := range rules {
		if r.Action == domain.ActionContinue || r.Action == domain.ActionSpend {
			return true
		}
	}
	return false
}

func hookKey(r domain.Rule, harness string) string {
	if r.Action == domain.ActionSubagentModel && harness == HarnessClaudeCode {
		return claudeSubagentHook
	}
	return harness
}

func ruleNote(r domain.Rule, rs RuleStatus) string {
	var notes []string
	switch {
	case r.TeamRouted():
		notes = append(notes, "set by your organisation; approval goes to its "+r.Require.Role+" role")
	case r.Require.Kind == domain.RequireApprover:
		notes = append(notes, "approver:"+r.Require.Role+" is answered on this machine (as ask): only organisation rules are routed to a role")
	case r.Source == domain.SourceOrg:
		notes = append(notes, "set by your organisation")
	}
	if !r.Enforced() && r.Source == domain.SourceOrg {
		notes = append(notes, "in shadow until your organisation enforces it")
	} else if !r.Enforced() {
		if rs.Matches == 0 {
			notes = append(notes, "in shadow and has not matched yet")
		} else {
			notes = append(notes, "in shadow; `tokenops boundaries promote "+r.Name+"` makes it bind")
		}
	}
	return strings.Join(notes, "; ")
}

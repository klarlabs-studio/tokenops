package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/opencodeplugin"
)

// HookVerb is the command every refusing hook runs; it is also the marker
// the installers and this status find the hook by.
const HookVerb = "boundary-guard"

// claudeSubagentHook keys Installed's answer for route-guard's Agent
// hook, which evaluates subagent_model rules in Claude Code.
const claudeSubagentHook = "claude-code/subagent"

// Enforcement is what a rule does in one harness.
const (
	EnforcementEnforced     = "enforced"
	EnforcementShadow       = "shadow"
	EnforcementAdviceOnly   = "advice only"
	EnforcementNotInstalled = "hook not installed"
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
}

// Status is every rule and every decision waiting on the operator.
type Status struct {
	Rules   []RuleStatus  `json:"rules"`
	Pending []PendingView `json:"pending"`
	Note    string        `json:"note,omitempty"`
}

// Installed reports, per harness that can refuse, whether its boundary
// hook is wired: Claude Code's settings.json and opencode's generated
// plugin. Empty paths take the defaults.
func Installed(claudeSettings, opencodePluginDir string) map[string]bool {
	home, _ := os.UserHomeDir()
	if claudeSettings == "" {
		claudeSettings = filepath.Join(home, ".claude", "settings.json")
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
			Harnesses: harnesses(r, installed),
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
		out.Pending = append(out.Pending, PendingView{
			ID: p.ID, Rule: p.Rule, Require: p.Require, Crossed: p.Crossed, Harness: p.Harness,
			SessionID: p.SessionID, At: p.At, Approve: "tokenops approve " + p.ID,
		})
	}
	switch {
	case len(rules) == 0:
		out.Note = "no boundaries configured; add a boundaries: section to config.yaml"
	case !installed[HarnessClaudeCode] && !installed[HarnessOpencode]:
		out.Note = "no boundary hook is installed, so no rule is evaluated: run `tokenops hooks install --boundary`"
	}
	return out, nil
}

func harnesses(r domain.Rule, installed map[string]bool) []HarnessStatus {
	out := make([]HarnessStatus, 0, 4)
	for _, h := range []string{HarnessClaudeCode, HarnessOpencode} {
		hs := HarnessStatus{Harness: h}
		switch {
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
		out = append(out, HarnessStatus{
			Harness: h, Enforcement: EnforcementAdviceOnly,
			Note: "its hooks cannot refuse a call, so the rule is not enforced there",
		})
	}
	return out
}

func hookKey(r domain.Rule, harness string) string {
	if r.Action == domain.ActionSubagentModel && harness == HarnessClaudeCode {
		return claudeSubagentHook
	}
	return harness
}

func ruleNote(r domain.Rule, rs RuleStatus) string {
	var notes []string
	if r.Require.Kind == domain.RequireApprover {
		notes = append(notes, "approver:"+r.Require.Role+" is answered on this machine for now (as ask); routing it to the role needs the team plane")
	}
	if !r.Enforced() {
		if rs.Matches == 0 {
			notes = append(notes, "in shadow and has not matched yet")
		} else {
			notes = append(notes, "in shadow; `tokenops boundaries promote "+r.Name+"` makes it bind")
		}
	}
	return strings.Join(notes, "; ")
}

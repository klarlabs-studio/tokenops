package cli

import "github.com/spf13/cobra"

// The help groups commands by what a person is doing, most used first,
// and leaves out what other programs run: the hooks Claude Code, Codex
// and opencode call, the MCP server, the daemon's foreground process, and
// the tools for working on TokenOps itself. Hidden commands run exactly as
// before; they are only absent from the list.

// helpGroups are the sections of `tokenops --help`, in order. Every day
// holds the five commands the product is used through (the review of
// 2026-11-10: about fifty top-level commands, five of them daily); the
// rest are grouped and run as before.
var helpGroups = []*cobra.Group{
	{ID: "everyday", Title: "Every day:"},
	{ID: "setup", Title: "Set up:"},
	{ID: "control", Title: "Coach, routing and policy:"},
	{ID: "analysis", Title: "Look closer:"},
}

// helpOrder lists the commands in each section most used first; the help
// keeps this order rather than sorting alphabetically.
var helpOrder = []string{
	"glance", "spend", "coach", "boundaries", "status",
	"init", "detect", "daemon", "hooks", "statusline", "menubar", "plan", "budget", "provider",
	"vendor-usage", "pricing", "otel", "team", "config", "version",
	"approve", "deny", "mode", "routing", "preferred-model", "experiment", "rules", "fmt", "task", "outcome",
	"scorecard", "checkup", "story", "dx", "explain", "optimizations", "verify", "audit", "events",
}

// commandGroup files each command under its section.
var commandGroup = map[string]string{
	"glance": "everyday", "spend": "everyday", "coach": "everyday", "boundaries": "everyday", "status": "everyday",

	"init": "setup", "detect": "setup", "daemon": "setup", "hooks": "setup", "statusline": "setup",
	"menubar": "setup", "plan": "setup", "budget": "setup", "provider": "setup",
	"vendor-usage": "setup", "config": "setup", "pricing": "setup", "version": "setup", "otel": "setup", "team": "setup",

	"approve": "control", "deny": "control", "mode": "control", "preferred-model": "control", "routing": "control",
	"experiment": "control", "task": "control", "outcome": "control", "rules": "control", "fmt": "control",

	"scorecard": "analysis", "checkup": "analysis", "story": "analysis", "dx": "analysis", "explain": "analysis",
	"optimizations": "analysis", "audit": "analysis",
	"events": "analysis", "verify": "analysis",
}

// hiddenCommands run as before but stay out of the help: other programs
// call them, or they are for working on TokenOps itself.
var hiddenCommands = map[string]string{
	"coach-hook":       "Claude Code's Stop hook",
	"read-guard":       "Claude Code's PreToolUse hook",
	"route-guard":      "Claude Code's UserPromptSubmit hook",
	"boundary-guard":   "the boundary pause point, Claude Code's, opencode's and Codex's PreToolUse hook",
	"anthropic-bridge": "launched by `hooks install`",
	"serve":            "the MCP server, started by the client",
	"start":            "the daemon's foreground process, started by `daemon install`",
	"coverage-debt":    "TokenOps' own test-coverage report",
}

// daemonOverrides are the root's daemon overrides. Every command that
// loads the config applies them, but they matter to few, so the flag lists
// leave them out and the root's help names them once.
var daemonOverrides = []string{"listen", "log-level", "log-format", "tls", "cert-dir"}

// organize orders and groups the commands, hides the plumbing, and keeps
// the daemon overrides out of the flag lists.
func organize(root *cobra.Command) {
	cobra.EnableCommandSorting = false
	reorder(root)
	root.AddGroup(helpGroups...)
	root.SetHelpCommandGroupID("setup")
	root.SetCompletionCommandGroupID("setup")
	for _, c := range root.Commands() {
		if g, ok := commandGroup[c.Name()]; ok {
			c.GroupID = g
		}
		if _, ok := hiddenCommands[c.Name()]; ok {
			c.Hidden = true
		}
	}
	for _, name := range daemonOverrides {
		_ = root.PersistentFlags().MarkHidden(name)
	}
	applyExamples(root)
}

// rootLong is the top of `tokenops --help`.
const rootLong = `TokenOps is guardrails for coding agents: it watches every AI plan and
account your agents draw on, steps in during the session before a limit,
a budget or a rule you set is crossed, and coaches how you use them. On
its own, ` + "`tokenops`" + ` shows every plan at a glance.

Start with ` + "`tokenops init`" + `: it sets up the plans, the client hooks and the
daemon. After that, the five commands under "Every day" are the ones
you use; the rest are grouped below and run as they always have.

Every command also takes the daemon's overrides, left out of the lists
below: --listen, --tls, --cert-dir, --log-level, --log-format.`

// reorder re-adds root's commands in helpOrder, then any others.
func reorder(root *cobra.Command) {
	byName := map[string]*cobra.Command{}
	all := root.Commands()
	for _, c := range all {
		byName[c.Name()] = c
	}
	root.RemoveCommand(all...)
	for _, name := range helpOrder {
		if c, ok := byName[name]; ok {
			root.AddCommand(c)
			delete(byName, name)
		}
	}
	for _, c := range all {
		if _, left := byName[c.Name()]; left {
			root.AddCommand(c)
		}
	}
}

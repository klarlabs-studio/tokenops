package cli

import "github.com/spf13/cobra"

// The help groups commands by what a person is doing, most used first,
// and leaves out what other programs run: the hooks Claude Code, Codex
// and opencode call, the MCP server, the daemon's foreground process, and
// the tools for working on TokenOps itself. Hidden commands run exactly as
// before; they are only absent from the list.

// helpGroups are the sections of `tokenops --help`, in order.
var helpGroups = []*cobra.Group{
	{ID: "everyday", Title: "Every day:"},
	{ID: "setup", Title: "Set up:"},
	{ID: "control", Title: "Coach, routing and policy:"},
	{ID: "analysis", Title: "Look closer:"},
}

// helpOrder lists the commands in each section most used first; the help
// keeps this order rather than sorting alphabetically.
var helpOrder = []string{
	"glance", "spend", "story", "dx", "status", "explain",
	"init", "detect", "daemon", "hooks", "statusline", "menubar", "plan", "budget", "provider",
	"vendor-usage", "pricing", "config", "version",
	"coach", "mode", "routing", "preferred-model", "experiment", "rules", "fmt", "task", "outcome",
	"scorecard", "optimizations", "verify", "audit", "events",
}

// commandGroup files each command under its section.
var commandGroup = map[string]string{
	"glance": "everyday", "spend": "everyday", "story": "everyday", "dx": "everyday",
	"status": "everyday", "explain": "everyday",

	"init": "setup", "detect": "setup", "daemon": "setup", "hooks": "setup", "statusline": "setup",
	"menubar": "setup", "plan": "setup", "budget": "setup", "provider": "setup",
	"vendor-usage": "setup", "config": "setup", "pricing": "setup", "version": "setup",

	"coach": "control", "mode": "control", "preferred-model": "control", "routing": "control",
	"experiment": "control", "task": "control", "outcome": "control", "rules": "control", "fmt": "control",

	"scorecard": "analysis", "optimizations": "analysis", "audit": "analysis",
	"events": "analysis", "verify": "analysis",
}

// hiddenCommands run as before but stay out of the help: other programs
// call them, or they are for working on TokenOps itself.
var hiddenCommands = map[string]string{
	"coach-hook":       "Claude Code's Stop hook",
	"read-guard":       "Claude Code's PreToolUse hook",
	"route-guard":      "Claude Code's UserPromptSubmit hook",
	"anthropic-bridge": "launched by `hooks install`",
	"serve":            "the MCP server, started by the client",
	"start":            "the daemon's foreground process, started by `daemon install`",
	"eval":             "TokenOps' own optimizer eval",
	"coverage-debt":    "TokenOps' own test-coverage report",
	"replay":           "optimizer development",
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
const rootLong = `TokenOps watches your AI coding plans, sessions and spend, and coaches
how you use them. On its own, ` + "`tokenops`" + ` shows every plan at a glance.

Start here:
  tokenops init            set up this machine (plans, hooks, the daemon)
  tokenops                 every plan's windows, pace, cost and the coach's findings
  tokenops spend           what you spent and where it went
  tokenops story           what you asked for and what the agent did
  tokenops coach           how much the coach says and does

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

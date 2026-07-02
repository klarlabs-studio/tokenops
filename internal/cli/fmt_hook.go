package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/formatter"
)

// newFmtHookCmd prints a shell snippet that transparently routes the
// formatter-backed commands through `tokenops fmt` — the RTK-style
// auto-rewrite, but opt-in and reversible. Each wrapper is gated on the
// TOKENOPS_FMT environment variable so compression only activates where you
// want it (typically inside an agent session), leaving interactive shell use
// untouched:
//
//	eval "$(tokenops fmt hook)"      # add to ~/.zshrc or ~/.bashrc
//	export TOKENOPS_FMT=1            # the agent sets this to activate
//
// The wrappers call `command <cmd>` (bypassing the function) via tokenops
// fmt, which execs the real binary by PATH lookup — shell functions are not
// on PATH, so there is no recursion.
func newFmtHookCmd(rf *rootFlags) *cobra.Command {
	var (
		shell    string
		commands []string
		level    string
	)
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Print a shell snippet that routes common commands through `tokenops fmt`",
		Long: `hook emits shell wrapper functions for the commands with a
dedicated formatter (git, go, npm, cargo, pytest, docker). Source it from
your shell rc and set TOKENOPS_FMT=1 to activate compression for the current
shell (e.g. an agent session); unset it to pass commands through untouched.

  eval "$(tokenops fmt hook)"
  export TOKENOPS_FMT=1

Scope to specific commands with --commands, and set a default level with
--level (falls back to config command_fmt.default when unset).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if shell != "bash" && shell != "zsh" {
				return fmt.Errorf("hook: --shell must be bash or zsh (got %q)", shell)
			}
			if len(commands) == 0 {
				commands = formatterCommands(rf)
			}
			sort.Strings(commands)

			levelArg := ""
			if level != "" {
				if _, ok := formatter.ParseLossLevel(level); !ok {
					return fmt.Errorf("hook: invalid --level %q", level)
				}
				levelArg = " --level " + level
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "# tokenops fmt hook (%s) — set TOKENOPS_FMT=1 to activate\n", shell)
			for _, c := range commands {
				// POSIX function form works for both bash and zsh.
				fmt.Fprintf(out,
					"%s() { if [ -n \"$TOKENOPS_FMT\" ]; then command tokenops fmt%s -- %s \"$@\"; else command %s \"$@\"; fi; }\n",
					c, levelArg, c, c)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&shell, "shell", "zsh", "target shell (bash|zsh)")
	cmd.Flags().StringSliceVar(&commands, "commands", nil, "restrict wrappers to these commands (default: all formatter-backed commands)")
	cmd.Flags().StringVar(&level, "level", "", "loss level baked into the wrappers (conservative|balanced|aggressive)")
	return cmd
}

// formatterCommands returns the command tokens with a dedicated formatter
// (built-in + user config), derived from the registered set so the hook
// stays in sync with the catalog.
func formatterCommands(rf *rootFlags) []string {
	reg := formatter.NewRegistry(formatter.LossPolicy{}, registryFormatters(rf)...)
	cmds := reg.Commands()
	// Drop the generic sentinel (empty token) if present.
	out := cmds[:0]
	for _, c := range cmds {
		if strings.TrimSpace(c) != "" {
			out = append(out, c)
		}
	}
	return out
}

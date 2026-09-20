package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/authority"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
)

// The settings below were reachable from an agent and not from a terminal.
// tokenops_mode, tokenops_preferred_model, tokenops_budget_set and
// tokenops_routing_rule_set all write config.yaml through the MCP server,
// while `tokenops config` could only print. An operator wanting any of them
// had to hand-edit YAML — the tool was easier to drive by asking an agent
// than by using it.

// newModeCmd reads or sets the operating mode.
func newModeCmd() *cobra.Command {
	var (
		configPathFlag string
		noRestartFlag  bool
	)
	cmd := &cobra.Command{
		Use:   "mode [passive|active]",
		Short: "Show or set the operating mode",
		Long: `mode with no argument prints the current mode.

  passive  collect and analyse on demand (default)
  active   passive, plus live interventions: routing rules applied to
           proxied traffic, and a watcher evaluating budgets

Active mode does its work inside the daemon, so it is a no-op without one
running.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				printAuthority(out, cfg)
				fmt.Fprintf(out, "\n  budgets:       %d\n", len(cfg.Budgets))
				fmt.Fprintf(out, "  routing rules: %d\n", len(cfg.Optimizer.RoutingRules))
				return nil
			}
			want, err := config.ParseMode(args[0])
			if err != nil {
				return err
			}
			cfg.Mode = want
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(out, "set mode = %s\nwrote %s\n", want, path)
			if want == config.ModeActive {
				fmt.Fprintln(out, "note: active mode runs inside the daemon — without one, nothing intervenes")
			}
			applyRestart(out, !noRestartFlag, true)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

// newPreferredModelCmd manages the per-provider model ceiling.
func newPreferredModelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preferred-model",
		Short: "Show or set the model ceiling a provider may not be routed above",
		Long: `The preferred model is a ceiling, not a default. A routing rule that
would move you to a pricier model is refused and referred to you; routes
to cheaper models still apply on their own.`,
	}
	cmd.AddCommand(newPreferredModelListCmd(), newPreferredModelSetCmd(), newPreferredModelUnsetCmd())
	return cmd
}

func newPreferredModelListCmd() *cobra.Command {
	var configPathFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the preferred model per provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(cfg.PreferredModels) == 0 {
				fmt.Fprintln(out, "no preferred models set — routing is bounded only by the rules themselves")
				return nil
			}
			names := make([]string, 0, len(cfg.PreferredModels))
			for p := range cfg.PreferredModels {
				names = append(names, p)
			}
			sort.Strings(names)
			for _, p := range names {
				fmt.Fprintf(out, "  %-12s %s\n", p, cfg.PreferredModels[p])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	return cmd
}

func newPreferredModelSetCmd() *cobra.Command {
	var (
		configPathFlag string
		noRestartFlag  bool
	)
	cmd := &cobra.Command{
		Use:   "set <provider> <model>",
		Short: "Set the ceiling for a provider",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			if cfg.PreferredModels == nil {
				cfg.PreferredModels = map[string]string{}
			}
			cfg.PreferredModels[args[0]] = args[1]
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set preferred_models.%s = %s\nwrote %s\n", args[0], args[1], path)
			applyRestart(cmd.OutOrStdout(), !noRestartFlag, true)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

func newPreferredModelUnsetCmd() *cobra.Command {
	var (
		configPathFlag string
		noRestartFlag  bool
	)
	cmd := &cobra.Command{
		Use:   "unset <provider>",
		Short: "Remove a provider's ceiling",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			if _, ok := cfg.PreferredModels[args[0]]; !ok {
				fmt.Fprintf(cmd.OutOrStdout(), "preferred_models.%s not set; nothing to do\n", args[0])
				return nil
			}
			delete(cfg.PreferredModels, args[0])
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed preferred_models.%s\nwrote %s\n", args[0], path)
			applyRestart(cmd.OutOrStdout(), !noRestartFlag, true)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

// newRoutingCmd groups the routing surfaces that had no CLI.
func newRoutingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "routing",
		Short: "Inspect routing proposals and manage routing rules",
	}
	cmd.AddCommand(newRoutingProposalsCmd(), newRoutingRuleCmd())
	return cmd
}

func newRoutingProposalsCmd() *cobra.Command {
	var storePath string
	cmd := &cobra.Command{
		Use:   "proposals",
		Short: "List model upgrades refused because they exceed your preferred model",
		Long: `Each entry is a real choice that is waiting on you: take the proposed
model, or stay on the preferred one. Nothing applies until you answer.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := storePath
			if path == "" {
				p, err := routingapproval.DefaultPath()
				if err != nil {
					return err
				}
				path = p
			}
			store, err := routingapproval.Open(path)
			if err != nil {
				return err
			}
			pending, err := store.Pending()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(pending) == 0 {
				fmt.Fprintln(out, "no upgrades are waiting on you")
				return nil
			}
			for _, p := range pending {
				fmt.Fprintf(out, "  %s\n", p.Key)
				fmt.Fprintf(out, "    %s: %s -> %s (preferred: %s), seen %d time(s)\n",
					p.Provider, p.From, p.To, p.Preferred, p.Seen)
				if p.Reason != "" {
					fmt.Fprintf(out, "    reason: %s\n", p.Reason)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&storePath, "store", "", "override the approvals store path")
	return cmd
}

func newRoutingRuleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule",
		Short: "List, set or remove model-routing rules",
	}
	cmd.AddCommand(newRoutingRuleListCmd(), newRoutingRuleSetCmd(), newRoutingRuleUnsetCmd())
	return cmd
}

func newRoutingRuleListCmd() *cobra.Command {
	var configPathFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured routing rules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(cfg.Optimizer.RoutingRules) == 0 {
				fmt.Fprintln(out, "no routing rules configured")
				return nil
			}
			for _, r := range cfg.Optimizer.RoutingRules {
				fmt.Fprintf(out, "  %-10s %s -> %s (quality %.2f)\n", r.Provider, r.FromModel, r.ToModel, r.Quality)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	return cmd
}

func newRoutingRuleSetCmd() *cobra.Command {
	var (
		configPathFlag string
		noRestartFlag  bool
		quality        float64
		fallbacks      []string
	)
	cmd := &cobra.Command{
		Use:   "set <provider> <from-model> <to-model>",
		Short: "Add or update a routing rule (upsert by provider + from-model)",
		Long: `A trailing * in from-model is a prefix match, e.g. claude-fable-5*.

Rules show would-be savings in ` + "`tokenops replay`" + `; with mode=active the
proxy rewrites matching live requests.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			rule := config.RoutingRuleConfig{
				Provider:  args[0],
				FromModel: args[1],
				ToModel:   args[2],
				Quality:   quality,
				Fallbacks: fallbacks,
			}
			// The same check tokenops_routing_rule_set runs, before the
			// file is touched, so the refusal names the argument rather
			// than an index into config.yaml.
			if err := rule.Validate(); err != nil {
				return fmt.Errorf("routing rule: %w", err)
			}
			replaced := false
			for i, r := range cfg.Optimizer.RoutingRules {
				if r.Provider == rule.Provider && r.FromModel == rule.FromModel {
					cfg.Optimizer.RoutingRules[i] = rule
					replaced = true
					break
				}
			}
			if !replaced {
				cfg.Optimizer.RoutingRules = append(cfg.Optimizer.RoutingRules, rule)
			}
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			verb := "added"
			if replaced {
				verb = "updated"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s rule %s: %s -> %s\nwrote %s\n",
				verb, rule.Provider, rule.FromModel, rule.ToModel, path)
			applyRestart(cmd.OutOrStdout(), !noRestartFlag, true)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	cmd.Flags().Float64Var(&quality, "quality", 0, "confidence (0-1] that the target preserves task quality")
	cmd.Flags().StringSliceVar(&fallbacks, "fallback", nil, "fallback model if the target is unavailable (repeatable)")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

func newRoutingRuleUnsetCmd() *cobra.Command {
	var (
		configPathFlag string
		noRestartFlag  bool
	)
	cmd := &cobra.Command{
		Use:   "unset <provider> <from-model>",
		Short: "Remove the routing rule matching provider + from-model",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			kept := cfg.Optimizer.RoutingRules[:0]
			removed := false
			for _, r := range cfg.Optimizer.RoutingRules {
				if r.Provider == args[0] && r.FromModel == args[1] {
					removed = true
					continue
				}
				kept = append(kept, r)
			}
			if !removed {
				fmt.Fprintf(cmd.OutOrStdout(), "no rule for %s %s; nothing to do\n", args[0], args[1])
				return nil
			}
			cfg.Optimizer.RoutingRules = kept
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed rule %s: %s\nwrote %s\n", args[0], args[1], path)
			applyRestart(cmd.OutOrStdout(), !noRestartFlag, true)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

// printAuthority answers "what can TokenOps do without me".
//
// The command used to print the daemon's rung and a count of budgets and
// routing rules — which answers "which of the five settings did I last
// touch". The other four, written in three other vocabularies, were
// nowhere, and one of them is in no config file at all.
func printAuthority(out io.Writer, cfg config.Config) {
	a := authority.Report(cfg)

	headline := "observes and advises; changes nothing without asking"
	if a.AnythingActs() {
		headline = "may act without asking — see the table below"
	}
	mode := cfg.Mode
	if mode == "" {
		mode = config.ModePassive
	}
	fmt.Fprintf(out, "mode: %s (%s) — %s\n\n", strings.ToLower(mode), a.Daemon, headline)

	fmt.Fprintf(out, "  %-18s %-16s %-26s %s\n", "SUBSYSTEM", "CONFIGURED", "EFFECTIVE", "SETTING")
	for _, s := range a.Subsystems {
		effective := s.Effective.String()
		if s.HeldBack() {
			// The marker goes beside the rung it explains, not at the end
			// of the row. The setting column is long enough that a note
			// after it is read last, if at all — and this is the thing an
			// operator most needs to notice, because it is what will
			// change the moment they raise the mode.
			effective += " (held back)"
		}
		fmt.Fprintf(out, "  %-18s %-16s %-26s %s\n",
			s.Name, s.Configured, effective, s.Setting)
	}
}

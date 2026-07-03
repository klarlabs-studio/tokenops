package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// knownProviderNames returns the sorted canonical IDs of every provider the
// proxy can route, so the CLI can validate `provider set` names up front
// instead of letting an unknown key hard-fail the daemon at boot.
func knownProviderNames() []string {
	names := make([]string, 0, len(providers.All()))
	for _, p := range providers.All() {
		names = append(names, string(p.ID))
	}
	sort.Strings(names)
	return names
}

func newProviderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Manage upstream LLM provider URLs in config.yaml",
		Long: `provider mutates the providers map in config.yaml so the daemon routes
upstream traffic without requiring an environment-variable export or a
manual file edit. Subcommands:

  tokenops provider list                          — show configured providers
  tokenops provider set <name> <url>              — bind <name> to <url>
  tokenops provider unset <name>                  — remove binding`,
	}
	cmd.AddCommand(newProviderListCmd(), newProviderSetCmd(), newProviderUnsetCmd())
	return cmd
}

func newProviderListCmd() *cobra.Command {
	var configPathFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List providers configured in config.yaml",
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
			if len(cfg.Providers) == 0 {
				fmt.Fprintln(out, "no provider URL overrides configured (all providers use their preset)")
			} else {
				fmt.Fprintln(out, "Configured overrides:")
				names := make([]string, 0, len(cfg.Providers))
				for name := range cfg.Providers {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					fmt.Fprintf(out, "  %-12s %s\n", name, cfg.Providers[name])
				}
			}
			fmt.Fprintln(out, "\nAvailable presets (bind with 'tokenops provider set <name>'):")
			for _, p := range providers.All() {
				marker := ""
				if _, overridden := cfg.Providers[string(p.ID)]; overridden {
					marker = "  [override set]"
				}
				fmt.Fprintf(out, "  %-12s %s%s\n", p.ID, p.DefaultBaseURL, marker)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	return cmd
}

func newProviderSetCmd() *cobra.Command {
	var configPathFlag string
	cmd := &cobra.Command{
		Use:   "set <name> [url]",
		Short: "Bind a known provider to an upstream URL (omit url to use the built-in preset)",
		Long: `set binds a provider name to an upstream base URL in config.yaml.

The name must be one of the providers the proxy can route (see
'tokenops provider list'); an unknown name is rejected here rather than
crashing the daemon at boot. Omit the URL to use the provider's built-in
preset base URL — e.g. 'tokenops provider set groq' binds the Groq
OpenAI-compatible endpoint.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			preset, ok := providers.Lookup(eventschema.Provider(name))
			if !ok {
				return fmt.Errorf(
					"unknown provider %q; known providers: %s",
					name, strings.Join(knownProviderNames(), ", "),
				)
			}
			url := preset.DefaultBaseURL
			if len(args) == 2 {
				url = args[1]
			}
			if _, err := providers.ParseUpstream(url); err != nil {
				return err
			}
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			if cfg.Providers == nil {
				cfg.Providers = map[string]string{}
			}
			cfg.Providers[name] = url
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			presetNote := ""
			if len(args) == 1 {
				presetNote = " (preset)"
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"set providers.%s = %s%s\nwrote %s\nnext: restart the daemon\n",
				name, url, presetNote, path,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	return cmd
}

func newProviderUnsetCmd() *cobra.Command {
	var configPathFlag string
	cmd := &cobra.Command{
		Use:   "unset <name>",
		Short: "Remove a provider binding",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			if _, ok := cfg.Providers[name]; !ok {
				fmt.Fprintf(cmd.OutOrStdout(), "providers.%s not set; nothing to do\n", name)
				return nil
			}
			delete(cfg.Providers, name)
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"removed providers.%s\nwrote %s\nnext: restart the daemon\n",
				name, path,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	return cmd
}

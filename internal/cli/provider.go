package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

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
			if len(cfg.Providers) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no providers configured")
				return nil
			}
			for name, url := range cfg.Providers {
				fmt.Fprintf(cmd.OutOrStdout(), "%-12s %s\n", name, url)
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
		Use:   "set <name> <url>",
		Short: "Bind a provider name to an upstream URL",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, url := args[0], args[1]
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
			fmt.Fprintf(cmd.OutOrStdout(),
				"set providers.%s = %s\nwrote %s\nnext: restart the daemon\n",
				name, url, path,
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

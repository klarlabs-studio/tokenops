package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newConfigCmd(rf *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect TokenOps configuration",
	}
	cmd.AddCommand(newConfigShowCmd(rf))
	return cmd
}

func newConfigShowCmd(rf *rootFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print effective configuration after defaults, file, env, and flags",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(cfg)
			}
			data, err := yaml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("marshal yaml: %w", err)
			}
			_, err = out.Write(data)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of YAML")
	return cmd
}

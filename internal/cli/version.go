package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/version"
)

// buildInfo is `tokenops version --json`.
type buildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func newVersionCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if jsonOut {
				return writeControlJSON(cmd, buildInfo{Version: version.Version, Commit: version.Commit, Date: version.Date})
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "tokenops", version.String())
			return err
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

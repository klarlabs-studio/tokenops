package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "tokenops", version.String())
			return err
		},
	}
}

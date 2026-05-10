package cli

import (
	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/daemon"
)

func newStartCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the TokenOps daemon in the foreground",
		Long: `Start runs the TokenOps daemon in the foreground until SIGINT or
SIGTERM is received. Configuration is resolved from --config (or the
TOKENOPS_* environment variables) with command-line flags applied last.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			ctx, stop := daemon.SignalContext(cmd.Context())
			defer stop()
			return daemon.Run(ctx, cfg, cmd.ErrOrStderr())
		},
	}
}

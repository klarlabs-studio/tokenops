// Package cli builds the tokenops command-line interface. The root command
// composes daemon lifecycle (start), control queries (status), config
// inspection, and version metadata into a single cobra tree shared by the
// tokenops binary and tests.
package cli

import (
	"github.com/spf13/cobra"
)

// rootFlags holds the common flags inherited by every subcommand. They are
// exposed as persistent flags on the root command so users can place them
// before or after the subcommand verb.
type rootFlags struct {
	configPath string
	listen     string
	logLevel   string
	logFormat  string
	tlsFlag    string // "" = no override, "true"|"false"
	certDir    string
}

// NewRoot returns the tokenops root command. It is a fresh tree per call so
// tests can drive isolated executions without leaking state between runs.
func NewRoot() *cobra.Command {
	rf := &rootFlags{}

	cmd := &cobra.Command{
		Use:           "tokenops",
		Short:         "TokenOps command-line interface",
		Long:          "tokenops manages the local TokenOps daemon and queries its control endpoints.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")

	cmd.PersistentFlags().StringVarP(&rf.configPath, "config", "c", "", "path to config.yaml")
	cmd.PersistentFlags().StringVar(&rf.listen, "listen", "", "override listen address (host:port)")
	cmd.PersistentFlags().StringVar(&rf.logLevel, "log-level", "", "override log level (debug|info|warn|error)")
	cmd.PersistentFlags().StringVar(&rf.logFormat, "log-format", "", "override log format (json|text)")
	cmd.PersistentFlags().StringVar(&rf.tlsFlag, "tls", "", "force TLS on/off (true|false)")
	cmd.PersistentFlags().StringVar(&rf.certDir, "cert-dir", "", "override TLS cert directory")

	cmd.AddCommand(
		newInitCmd(),
		newDemoCmd(),
		newStartCmd(rf),
		newServeCmd(),
		newStatusCmd(rf),
		newVersionCmd(),
		newConfigCmd(rf),
		newAuditCmd(rf),
		newCoverageDebtCmd(),
		newEvalCmd(),
		newEventsCmd(rf),
		newReplayCmd(rf),
		newRulesCmd(),
		newScorecardCmd(),
		newSpendCmd(rf),
		newPlanCmd(rf),
		newProviderCmd(),
	)
	return cmd
}

// Execute is the convenience entry point for cmd/tokenops.
func Execute() error {
	return NewRoot().Execute()
}

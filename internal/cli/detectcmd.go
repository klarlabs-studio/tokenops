package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/cli/detect"
)

// newDetectCmd reports what is installed and changes nothing.
//
// It exists because `tokenops init --detect` did not. That flag reads as a
// dry run — its help says it will "sniff installed AI clients and report
// likely plan bindings" — but it is an add-on to `init`, which registers the
// MCP server and installs hooks whether or not it is passed. An operator who
// wanted to know what was on their machine got their client configuration
// written instead.
//
// The flag is not the thing to change: someone running `init --detect` wants
// both, and `--print-only` and `--no-wire` already exist for the dry runs.
// What was missing was a way to ask the question on its own, so asking it now
// has its own verb, and it is the one the flag's help points at.
func newDetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Report which AI clients are installed, and change nothing",
		Long: `detect sniffs the machine for installed AI clients and reports the
plan bindings they imply. It reads the filesystem and environment only:
no network calls, no credential reads, and nothing is written.

To act on what it finds, run the printed 'tokenops plan set' lines, or
'tokenops init' to wire everything up.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			found := detect.Detect(nil)
			renderDetection(out, found)
			if len(found) > 0 {
				fmt.Fprintln(out, "\nNothing was changed. Run `tokenops init` to wire the MCP server and hooks.")
			}
			return nil
		},
	}
}

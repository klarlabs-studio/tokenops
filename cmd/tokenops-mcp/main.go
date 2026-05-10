// Command tokenops-mcp is the TokenOps MCP server (deprecated — use
// "tokenops serve" instead). Delegates to the serve subcommand so
// existing tooling continues to work.
package main

import (
	"fmt"
	"os"

	"github.com/felixgeelhaar/tokenops/internal/cli"
)

func main() {
	cmd := cli.NewRoot()
	cmd.SetArgs([]string{"serve"})
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

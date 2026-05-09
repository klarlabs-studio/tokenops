// Command tokenops is the TokenOps command-line interface.
//
// CLI subcommands are implemented in the dedicated cli-skeleton task; this
// entry point currently emits build metadata and exits, providing a buildable
// binary for the bootstrap milestone.
package main

import (
	"fmt"
	"os"

	"github.com/felixgeelhaar/tokenops/internal/version"
)

func main() {
	if _, err := fmt.Fprintf(os.Stdout, "tokenops %s\n", version.String()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

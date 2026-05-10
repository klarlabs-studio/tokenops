// Command tokenops is the TokenOps command-line interface. The cobra
// command tree lives in internal/cli; this entry point exists so the
// binary can be built and so errors surface as clean process exits.
package main

import (
	"fmt"
	"os"

	"github.com/felixgeelhaar/tokenops/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

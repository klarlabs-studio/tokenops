// Command tokenopsd is the TokenOps daemon.
//
// The daemon hosts the local proxy, optimization engine, observability
// platform, and supporting subsystems. The proxy-skeleton task wires up the
// HTTP server, configuration, and graceful shutdown; this entry point
// currently emits build metadata so the binary is buildable from day one.
package main

import (
	"fmt"
	"os"

	"github.com/felixgeelhaar/tokenops/internal/version"
)

func main() {
	if _, err := fmt.Fprintf(os.Stdout, "tokenopsd %s\n", version.String()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

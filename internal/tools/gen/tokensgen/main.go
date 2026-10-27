// Command tokensgen writes what assets/brand/tokens.json generates: the
// CSS custom properties in assets/brand/tokens.css and the menu bar's copy
// of it, and the token table in assets/brand/README.md.
//
//	go generate ./internal/tools/gen/designtokens
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/tools/gen"
	"go.klarlabs.de/tokenops/internal/tools/gen/designtokens"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := run(*root); err != nil {
		fmt.Fprintln(os.Stderr, "tokensgen:", err)
		os.Exit(1)
	}
}

func run(root string) error {
	f, err := designtokens.Load(filepath.Join(root, designtokens.Source))
	if err != nil {
		return err
	}
	css, err := designtokens.CSS(f)
	if err != nil {
		return err
	}
	for _, path := range designtokens.Outputs {
		if err := gen.WriteIfChanged(filepath.Join(root, path), css); err != nil {
			return err
		}
	}
	table, err := designtokens.Table(f)
	if err != nil {
		return err
	}
	readme := filepath.Join(root, designtokens.Readme)
	doc, err := os.ReadFile(readme)
	if err != nil {
		return err
	}
	out, err := gen.ReplaceRegion(doc, designtokens.RegionName, table)
	if err != nil {
		return fmt.Errorf("%s: %w", designtokens.Readme, err)
	}
	return gen.WriteIfChanged(readme, out)
}

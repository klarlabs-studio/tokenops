// Command providerdocs writes what the provider registry generates: the
// provider tables in the docs (between "<!-- begin generated: name -->"
// markers) and the menu bar's provider names and logo list.
//
//	go run go.klarlabs.de/tokenops/internal/tools/gen/providerdocs -root .
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/tools/gen"
	"go.klarlabs.de/tokenops/internal/tools/gen/providercatalog"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := run(*root); err != nil {
		fmt.Fprintln(os.Stderr, "providerdocs:", err)
		os.Exit(1)
	}
}

func run(root string) error {
	for _, r := range providercatalog.Regions() {
		path := filepath.Join(root, r.File)
		doc, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := gen.ReplaceRegion(doc, r.Name, r.Body)
		if err != nil {
			return fmt.Errorf("%s: %w", r.File, err)
		}
		if err := gen.WriteIfChanged(path, out); err != nil {
			return err
		}
	}
	files, err := providercatalog.Files()
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := gen.WriteIfChanged(filepath.Join(root, f.Path), f.Body); err != nil {
			return err
		}
	}
	return nil
}

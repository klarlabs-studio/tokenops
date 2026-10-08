// Command listgen writes a Go file listing every top-level function of a
// package with a given name prefix and result type, so a provider or a
// reader is registered by adding its file and running `go generate ./...`,
// with no init() and no hand-kept list.
//
//	//go:generate go run go.klarlabs.de/tokenops/internal/tools/gen/listgen -out registry_gen.go -list registered=provider:Descriptor
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"go.klarlabs.de/tokenops/internal/tools/gen"
)

type listFlags []gen.List

func (l *listFlags) String() string { return fmt.Sprint(*l) }
func (l *listFlags) Set(s string) error {
	v, err := gen.ParseList(s)
	if err != nil {
		return err
	}
	*l = append(*l, v)
	return nil
}

func main() {
	var lists listFlags
	out := flag.String("out", "", "file to write, in the current directory")
	dir := flag.String("dir", ".", "package directory")
	flag.Var(&lists, "list", "var=prefix:Type (repeatable)")
	flag.Parse()
	if *out == "" || len(lists) == 0 || strings.Contains(*out, "/") {
		fmt.Fprintln(os.Stderr, "usage: listgen -out file_gen.go -list var=prefix:Type ...")
		os.Exit(2)
	}
	b, err := gen.ListFile(*dir, *out, lists)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listgen:", err)
		os.Exit(1)
	}
	if err := gen.WriteIfChanged(*dir+"/"+*out, b); err != nil {
		fmt.Fprintln(os.Stderr, "listgen:", err)
		os.Exit(1)
	}
}

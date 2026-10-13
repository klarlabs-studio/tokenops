package accounts

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Every tokenops process links this package, including the hooks that run
// once per tool call, so a pattern compiled at package init is paid on
// every call whether or not its provider is read. Patterns go through
// lazyRegexp instead.
func TestNoRegexpCompiledAtPackageInit(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			ast.Inspect(gd, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if ok && strings.HasPrefix(sel.Sel.Name, "MustCompile") {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == "regexp" {
						t.Errorf("%s: regexp.%s in a package-level var; use lazyRegexp", fset.Position(sel.Pos()), sel.Sel.Name)
					}
				}
				return true
			})
		}
	}
}

func TestLazyRegexpCompilesOnce(t *testing.T) {
	re := lazyRegexp(`a+`)
	first := re()
	if second := re(); first != second {
		t.Fatal("lazyRegexp compiled twice")
	}
	if !re().MatchString("caa") {
		t.Fatal("lazyRegexp pattern does not match")
	}
}

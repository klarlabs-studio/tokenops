package accounts

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// readerSources parses and type-checks the readers in this directory, tests
// left out. Imports are not resolved: the figures a reader decodes are
// held in types declared here, which is all the rules below look at.
func readerSources(t *testing.T) (*token.FileSet, []*ast.File, *types.Info) {
	t.Helper()
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: unresolved{}, Error: func(error) {}}
	_, _ = conf.Check("accounts", fset, files, info)
	return fset, files, info
}

// unresolved answers every import with an empty package.
type unresolved struct{}

func (unresolved) Import(path string) (*types.Package, error) {
	name := path[strings.LastIndex(path, "/")+1:]
	pkg := types.NewPackage(path, name)
	pkg.MarkComplete()
	return pkg, nil
}

var _ types.Importer = unresolved{}

// isNumber reports whether e is a number or a pointer to one.
func isNumber(info *types.Info, e ast.Expr) bool {
	t := info.TypeOf(e)
	if t == nil {
		return false
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj().Name() == "number" && named.Obj().Pkg() != nil && named.Obj().Pkg().Name() == "accounts"
}

// TestAFigureIsReadOnlyWhereItsPresenceIs holds the readers to the lenient
// decoder's contract: a number carries a value and whether the vendor sent
// one, and a function that reads the value reads the flag of the same
// figure. A value read alone is how an absent figure becomes 0% used, an
// empty balance or a limit reached.
func TestAFigureIsReadOnlyWhereItsPresenceIs(t *testing.T) {
	fset, files, info := readerSources(t)
	var bad []string
	numbers := 0
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || onNumber(fn) {
				continue
			}
			checked := map[string]bool{}
			var values []*ast.SelectorExpr
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || !isNumber(info, sel.X) {
					return true
				}
				switch sel.Sel.Name {
				case "ok":
					checked[types.ExprString(sel.X)] = true
				case "v":
					values = append(values, sel)
				}
				return true
			})
			for _, sel := range values {
				numbers++
				if x := types.ExprString(sel.X); !checked[x] {
					bad = append(bad, fset.Position(sel.Pos()).String()+": "+x+".v is read in "+fn.Name.Name+", which never reads "+x+".ok")
				}
			}
		}
	}
	if numbers < 100 {
		t.Fatalf("only %d figure reads found in the readers: the check no longer sees them", numbers)
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
}

// onNumber reports whether fn is one of number's own methods.
func onNumber(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	t := fn.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	id, ok := t.(*ast.Ident)
	return ok && id.Name == "number"
}

// TestADecodedFigureCanBeAbsent holds every decimal figure a reader decodes
// from JSON to a type that can say the vendor sent none: a number, a stamp
// or a pointer. A plain float64 field decodes an absent figure as zero,
// which reads as a figure.
func TestADecodedFigureCanBeAbsent(t *testing.T) {
	fset, files, _ := readerSources(t)
	var bad []string
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range st.Fields.List {
				if field.Tag == nil || !strings.Contains(field.Tag.Value, "json:") {
					continue
				}
				id, ok := field.Type.(*ast.Ident)
				if !ok || (id.Name != "float64" && id.Name != "float32") {
					continue
				}
				name := ""
				if len(field.Names) > 0 {
					name = field.Names[0].Name
				}
				tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("json")
				bad = append(bad, fset.Position(field.Pos()).String()+": "+name+" ("+id.Name+", json "+tag+") cannot be absent: make it a number, a stamp or a pointer")
			}
			return true
		})
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
}

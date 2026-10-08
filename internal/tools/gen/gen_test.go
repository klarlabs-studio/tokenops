package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceRegion(t *testing.T) {
	doc := []byte("a\n" + BeginMarker("x") + "\nold\n" + EndMarker("x") + "\nb\n")
	got, err := ReplaceRegion(doc, "x", "new\n")
	if err != nil || string(got) != "a\n"+BeginMarker("x")+"\nnew\n"+EndMarker("x")+"\nb\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := ReplaceRegion(doc, "y", ""); err == nil {
		t.Error("missing markers accepted")
	}
}

func TestParseList(t *testing.T) {
	l, err := ParseList("readers=reader:usage.Reader")
	if err != nil || l != (List{Var: "readers", Prefix: "reader", Type: "usage.Reader"}) {
		t.Fatalf("%+v %v", l, err)
	}
	for _, bad := range []string{"", "x", "x=y", "=p:T"} {
		if _, err := ParseList(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// ListFile lists matching functions in file-name order, skips tests, other
// result types and the output file, and brings the result type's import.
func TestListFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b.go", "package p\n\nimport u \"example.com/usage\"\n\nfunc readerB() u.Reader { return nil }\nfunc readerC(x int) u.Reader { return nil }\n")
	write("a.go", "package p\n\nimport u \"example.com/usage\"\n\nfunc readerA() u.Reader { return nil }\nfunc reader() u.Reader { return nil }\nfunc readerX() int { return 0 }\n")
	write("a_test.go", "package p\n\nfunc readerT() u.Reader { return nil }\n")
	write("out_gen.go", "package p\n\nfunc readerOld() u.Reader { return nil }\n")
	b, err := ListFile(dir, "out_gen.go", []List{{Var: "readers", Prefix: "reader", Type: "u.Reader"}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "u \"example.com/usage\"") || !strings.Contains(s, "readerA,\n\treaderB,\n}") ||
		strings.Contains(s, "readerC") || strings.Contains(s, "readerT") || strings.Contains(s, "readerOld") || strings.Contains(s, "readerX") {
		t.Errorf("generated:\n%s", s)
	}
}

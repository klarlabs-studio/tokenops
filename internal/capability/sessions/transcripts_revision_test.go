package sessions

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var updateRevision = flag.Bool("update", false, "rewrite transcripts_revision.go")

const (
	revisionRoot = "internal/capability/sessions/transcriptcache.go"
	revisionFile = "transcripts_revision.go"
)

// A saved transcript cache is read back by any build with the same
// TranscriptsRevision, so the revision has to change whenever the code
// that fills the cache does. It is made from that code; this fails when
// the two have drifted apart, and -update writes the new one.
func TestTranscriptsRevisionIsCurrent(t *testing.T) {
	root, module := moduleRoot(t)
	files, err := readingCode(root, module, revisionRoot)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, f := range files {
		src, err := os.ReadFile(filepath.Join(root, f)) //nolint:gosec // this module's own source
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(h, "\x00%s\x00", f)
		if err := hashCode(h, src); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	got := hex.EncodeToString(h.Sum(nil))[:16]
	if *updateRevision {
		src, err := os.ReadFile(revisionFile)
		if err != nil {
			t.Fatal(err)
		}
		next := bytes.Replace(src, []byte(strconv.Quote(TranscriptsRevision)), []byte(strconv.Quote(got)), 1)
		if err := os.WriteFile(revisionFile, next, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if got != TranscriptsRevision {
		t.Fatalf("the code that reads transcripts changed (%s, recorded %s), so a cache saved before it must not be read back.\n"+
			"Run: go test ./internal/capability/sessions -run TestTranscriptsRevisionIsCurrent -update\n"+
			"and commit %s. The code is %d files, from %s.", got, TranscriptsRevision, revisionFile, len(files), revisionRoot)
	}
	// The readers are the point of it, and so is what they are built on:
	// a revision without either would never notice a reader change.
	for _, want := range []string{
		"internal/contexts/spend/vendorusage/jsonltail/tail.go",
		"internal/contexts/governance/agentdx/cache.go", "internal/contexts/governance/agentdx/codex.go",
		"internal/contexts/governance/agentdx/extract.go", "internal/contexts/governance/agentdx/persist.go",
		"internal/contexts/governance/story/continuation.go", "internal/contexts/optimization/taskclass/kind.go",
	} {
		if i := sort.SearchStrings(files, want); i == len(files) || files[i] != want {
			t.Errorf("%s is not part of the revision", want)
		}
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			t.Errorf("%s is a test and reads no transcript, but is part of the revision", f)
		}
	}
}

// Comments and spacing say nothing about what is read; everything else
// may.
func TestRevisionIsOfTheCodeNotItsComments(t *testing.T) {
	sum := func(src string) string {
		h := sha256.New()
		if err := hashCode(h, []byte(src)); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	base := sum("package p\n\n// f reads.\nfunc f() int { return 1 }\n")
	for name, tt := range map[string]struct {
		src  string
		same bool
	}{
		"another comment":      {"package p\n\n// f reads a line.\nfunc f() int { return 1 }\n", true},
		"no comment":           {"package p\n\nfunc f() int { return 1 }\n", true},
		"a comment in a line":  {"package p\n\nfunc f() int { return 1 /* one */ }\n", true},
		"other spacing":        {"package p\n\n\nfunc  f()  int  {  return 1  }\n", true},
		"another value":        {"package p\n\nfunc f() int { return 2 }\n", false},
		"another name":         {"package p\n\nfunc g() int { return 1 }\n", false},
		"a string that grew":   {"package p\n\nfunc f() int { _ = \"a\"; return 1 }\n", false},
		"a build constraint":   {"//go:build darwin\n\npackage p\n\nfunc f() int { return 1 }\n", false},
		"a statement split up": {"package p\n\nfunc f() int { return 1 }\n\nfunc g() {}\n", false},
	} {
		if got := sum(tt.src) == base; got != tt.same {
			t.Errorf("%s: same revision = %v, want %v", name, got, tt.same)
		}
	}
}

// moduleRoot is the directory holding go.mod and the module's path.
func moduleRoot(t *testing.T) (dir, module string) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		mod, err := os.ReadFile(filepath.Join(dir, "go.mod")) //nolint:gosec // this module's own go.mod
		if err == nil {
			for _, line := range strings.Split(string(mod), "\n") {
				if rest, ok := strings.CutPrefix(line, "module "); ok {
					return dir, strings.TrimSpace(rest)
				}
			}
			t.Fatalf("%s names no module", filepath.Join(dir, "go.mod"))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}

// readingCode is the file at start and every non-test Go file of every
// package of the module it reaches through imports, as sorted paths from
// the module's root.
func readingCode(root, module, start string) ([]string, error) {
	fset := token.NewFileSet()
	imports := func(file string) ([]string, error) {
		f, err := parser.ParseFile(fset, filepath.Join(root, file), nil, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		var dirs []string
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, err
			}
			if rest, ok := strings.CutPrefix(path, module+"/"); ok {
				dirs = append(dirs, rest)
			}
		}
		return dirs, nil
	}
	queue, err := imports(start)
	if err != nil {
		return nil, err
	}
	files, seen := []string{start}, map[string]bool{}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if seen[dir] {
			continue
		}
		seen[dir] = true
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			file := dir + "/" + name
			if file == start {
				continue
			}
			files = append(files, file)
			more, err := imports(file)
			if err != nil {
				return nil, err
			}
			queue = append(queue, more...)
		}
	}
	sort.Strings(files)
	return files, nil
}

// hashCode writes src's tokens to h: the code without its comments and
// its spacing. A compiler directive is a comment that changes the code,
// so it is kept. Where a line ends is kept too, since it can end a
// statement.
func hashCode(h hash.Hash, src []byte) error {
	fset := token.NewFileSet()
	var s scanner.Scanner
	var failed error
	s.Init(fset.AddFile("", fset.Base(), len(src)), src, func(pos token.Position, msg string) {
		if failed == nil {
			failed = fmt.Errorf("%s: %s", pos, msg)
		}
	}, scanner.ScanComments)
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return failed
		}
		if tok == token.COMMENT && !strings.HasPrefix(lit, "//go:") {
			continue
		}
		if tok == token.SEMICOLON {
			// One written out and one the scanner adds at a line's end
			// are the same code.
			lit = ""
		}
		fmt.Fprintf(h, "%d\x00%s\x00", tok, lit)
	}
}

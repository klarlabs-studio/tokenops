package version

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocsCodeSpansDoNotWrapAPlaceholder guards the docs build: VitePress
// compiles each page as a Vue template, and an inline code span that
// wraps across a line while holding a placeholder such as `<id>` can be
// read as an unclosed HTML tag, which fails the whole site build. Which
// wraps break depends on where the line falls, so none are allowed.
func TestDocsCodeSpansDoNotWrapAPlaceholder(t *testing.T) {
	root := "../../web/docs"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // repository docs
		if err != nil {
			return err
		}
		for _, bad := range wrappedSpans(string(b)) {
			t.Errorf("%s:%d: code span wraps before a placeholder; keep it on one line: %q", path, bad.line, bad.span)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type wrapped struct {
	line int
	span string
}

// wrappedSpans finds single-backtick code spans that cross a line break
// and hold a '<', paragraph by paragraph, outside fenced blocks.
func wrappedSpans(doc string) []wrapped {
	var out []wrapped
	lines := strings.Split(doc, "\n")
	fenced := false
	var para []string
	start := 0
	flush := func() {
		text := strings.Join(para, "\n")
		open := -1
		for i := 0; i < len(text); i++ {
			if text[i] != '`' {
				continue
			}
			if (i > 0 && text[i-1] == '`') || (i+1 < len(text) && text[i+1] == '`') {
				continue // double backticks and fences are not checked here
			}
			if open < 0 {
				open = i
				continue
			}
			span := text[open : i+1]
			if strings.Contains(span, "\n") && strings.Contains(span, "<") {
				out = append(out, wrapped{line: start + strings.Count(text[:open], "\n") + 1, span: span})
			}
			open = -1
		}
		para = nil
	}
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "```") {
			flush()
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		if len(para) == 0 {
			start = i
		}
		para = append(para, l)
	}
	flush()
	return out
}

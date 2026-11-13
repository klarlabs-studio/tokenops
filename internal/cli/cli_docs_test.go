package cli

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// cliDocsPath is the CLI reference the docs site serves.
const cliDocsPath = "../../docs/reference/cli.md"

// cobraBuiltins are commands cobra adds itself; they need no section.
var cobraBuiltins = map[string]bool{"help": true, "completion": true}

// cliDocsHeading matches a section heading that names a command, such as
// "### `tokenops plan {list|set}`", and captures the command.
var cliDocsHeading = regexp.MustCompile("(?m)^#{2,4} .*?`tokenops ([a-z][a-z-]*)")

// The CLI reference has a section for every command `tokenops --help`
// lists, and none for a command it does not: a command added without
// documenting it, documented and since removed, or hidden from the help
// fails here, as docs/api/openapi.json fails for the daemon's routes.
func TestCLIDocsPageCoversEveryCommand(t *testing.T) {
	page, err := os.ReadFile(filepath.Clean(cliDocsPath))
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range cliDocsHeading.FindAllStringSubmatch(string(page), -1) {
		documented[m[1]] = true
	}

	visible := map[string]bool{}
	hidden := map[string]bool{}
	for _, c := range NewRoot().Commands() {
		switch {
		case cobraBuiltins[c.Name()]:
		case c.Hidden:
			hidden[c.Name()] = true
		default:
			visible[c.Name()] = true
		}
	}

	for _, name := range slices.Sorted(maps.Keys(visible)) {
		if !documented[name] {
			t.Errorf("`tokenops %s` is in --help but has no section in docs/reference/cli.md", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(documented)) {
		switch {
		case hidden[name]:
			t.Errorf("docs/reference/cli.md has a section for `tokenops %s`, which --help hides", name)
		case !visible[name]:
			t.Errorf("docs/reference/cli.md has a section for `tokenops %s`, which is not a command", name)
		}
	}
}

// bareTag is a "<word" outside code: GitHub drops it as an unknown HTML
// tag, and the website, which builds these pages with VitePress, reads it
// as an unclosed element and fails to build.
var bareTag = regexp.MustCompile(`<[A-Za-z]`)

// inlineCode and autolink are the spans where a "<word" is shown as written.
var inlineCode = regexp.MustCompile("`[^`]*`|<https?://[^>]*>")

// The reference pages render the same on GitHub and on the website: a
// placeholder such as <sha> stays inside code.
func TestReferencePagesHaveNoBareTags(t *testing.T) {
	pages, err := filepath.Glob("../../docs/reference/*.md")
	if err != nil || len(pages) == 0 {
		t.Fatalf("no reference pages (%v)", err)
	}
	for _, page := range pages {
		b, err := os.ReadFile(filepath.Clean(page))
		if err != nil {
			t.Fatal(err)
		}
		fence := false
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				fence = !fence
				continue
			}
			if fence {
				continue
			}
			if bareTag.MatchString(inlineCode.ReplaceAllString(line, "")) {
				t.Errorf("%s:%d: a <placeholder> outside code: %s", filepath.Base(page), i+1, strings.TrimSpace(line))
			}
		}
	}
}

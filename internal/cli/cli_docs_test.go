package cli

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// cliDocsPath is the CLI reference the docs site serves.
const cliDocsPath = "../../web/docs/guide/cli.md"

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
			t.Errorf("`tokenops %s` is in --help but has no section in web/docs/guide/cli.md", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(documented)) {
		switch {
		case hidden[name]:
			t.Errorf("web/docs/guide/cli.md has a section for `tokenops %s`, which --help hides", name)
		case !visible[name]:
			t.Errorf("web/docs/guide/cli.md has a section for `tokenops %s`, which is not a command", name)
		}
	}
}

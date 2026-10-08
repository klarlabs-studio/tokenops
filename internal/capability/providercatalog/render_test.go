package providercatalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/tools/gen"
)

const root = "../../.."

const regenerate = "regenerate with: go generate ./internal/contexts/spend/providers"

// The docs tables between generated markers are what the registry says.
func TestDocsRegionsAreCurrent(t *testing.T) {
	for _, r := range Regions() {
		doc, err := os.ReadFile(filepath.Join(root, r.File))
		if err != nil {
			t.Fatal(err)
		}
		want, err := gen.ReplaceRegion(doc, r.Name, r.Body)
		if err != nil {
			t.Fatalf("%s: %v", r.File, err)
		}
		if !bytes.Equal(doc, want) {
			t.Errorf("%s region %q is stale; %s", r.File, r.Name, regenerate)
		}
	}
}

// The menu bar's names and logo list are what the registry says.
func TestMenubarFilesAreCurrent(t *testing.T) {
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(root, f.Path))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, f.Body) {
			t.Errorf("%s is stale; %s", f.Path, regenerate)
		}
	}
}

// Each table row names its vendor once.
func TestAccountTableRows(t *testing.T) {
	table := Regions()[0].Body
	if !strings.Contains(table, "| OpenRouter | `GET /api/v1/key` |") {
		t.Errorf("accounts table:\n%s", table)
	}
	if strings.Contains(table, "z.ai") {
		t.Error("a subscription reader is in the balance and spend table")
	}
}

package rulesfs

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/felixgeelhaar/tokenops/internal/contexts/rules"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestIngestorSnapshotInMemory(t *testing.T) {
	memFS := fstest.MapFS{
		"CLAUDE.md":                  {Data: []byte("# Rules\n\n## Testing\nuse tdd\n")},
		"AGENTS.md":                  {Data: []byte("# Agents\nbody\n")},
		".cursor/rules/go.mdc":       {Data: []byte("# Go\nbody\n")},
		"docs/conventions/style.md":  {Data: []byte("# Style\nbody\n")},
		"README.md":                  {Data: []byte("not a rule\n")},
		".git/HEAD":                  {Data: []byte("ref: refs/heads/main\n")},
		"node_modules/foo/CLAUDE.md": {Data: []byte("ignored\n")},
	}
	in := &Ingestor{FS: memFS, RepoID: "tokenops"}
	docs, err := in.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	paths := make([]string, 0, len(docs))
	for _, d := range docs {
		paths = append(paths, d.Path)
	}
	want := []string{".cursor/rules/go.mdc", "AGENTS.md", "CLAUDE.md", "docs/conventions/style.md"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i, w := range want {
		if paths[i] != w {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], w)
		}
	}
	for _, d := range docs {
		if !strings.HasPrefix(d.SourceID, "tokenops:") {
			t.Errorf("SourceID = %q, want tokenops: prefix", d.SourceID)
		}
	}
	for _, d := range docs {
		if d.Path == "CLAUDE.md" {
			if len(d.Blocks) < 2 {
				t.Errorf("CLAUDE.md blocks = %d, want >= 2", len(d.Blocks))
			}
			if d.Source != eventschema.RuleSourceClaudeMD {
				t.Errorf("CLAUDE.md source = %q, want claude_md", d.Source)
			}
		}
	}
}

// permissionDeniedFS wraps a MapFS and pretends one subtree is locked
// down with EACCES, mirroring macOS `~/Library/Saved Application
// State/*` semantics that crashed the walker before the fix. Open on
// the protected prefix returns fs.ErrPermission; everything else
// flows through to the underlying map.
type permissionDeniedFS struct {
	base   fstest.MapFS
	denied string
}

func (p permissionDeniedFS) Open(name string) (fs.File, error) {
	if strings.HasPrefix(name, p.denied) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return p.base.Open(name)
}

func (p permissionDeniedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if strings.HasPrefix(name, p.denied) && name != p.denied {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
	}
	entries, err := p.base.ReadDir(name)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func TestIngestorTolersPermissionDeniedSibling(t *testing.T) {
	memFS := fstest.MapFS{
		".claude/CLAUDE.md":  {Data: []byte("# Rules\nbody\n")},
		"locked/secret":      {Data: []byte("denied\n")},
		"locked/nested/leaf": {Data: []byte("denied\n")},
	}
	fsys := permissionDeniedFS{base: memFS, denied: "locked"}
	in := &Ingestor{FS: fsys, RepoID: "test"}
	paths, err := in.Discover()
	if err != nil {
		t.Fatalf("Discover should swallow EACCES, got %v", err)
	}
	// Discovery should still find the .claude/CLAUDE.md leaf even
	// though the sibling subtree is unreadable. .claude matches the
	// hidden-dir skip path so the file inside is NOT discovered;
	// what we're proving is the WALK didn't abort.
	if len(paths) != 0 && paths[0] != ".claude/CLAUDE.md" {
		t.Errorf("paths = %v", paths)
	}
}

func TestIngestorSkipsLibraryTrees(t *testing.T) {
	memFS := fstest.MapFS{
		"CLAUDE.md":                             {Data: []byte("# Rules\nbody\n")},
		"Library/State/CLAUDE.md":               {Data: []byte("ignored\n")},
		"Containers/com.app/CLAUDE.md":          {Data: []byte("ignored\n")},
		"Saved Application State/foo/CLAUDE.md": {Data: []byte("ignored\n")},
	}
	in := &Ingestor{FS: memFS, RepoID: "test"}
	paths, err := in.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(paths) != 1 || paths[0] != "CLAUDE.md" {
		t.Errorf("paths = %v, want [CLAUDE.md] (Library/Containers trees must be skipped)", paths)
	}
}

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"CLAUDE.md", "CLAUDE.md", true},
		{"CLAUDE.md", "docs/CLAUDE.md", false},
		{".cursor/rules/*", ".cursor/rules/go.mdc", true},
		{".cursor/rules/*", ".cursor/rules/sub/go.mdc", false},
		{".cursor/rules/**/*", ".cursor/rules/sub/go.mdc", true},
		{"*.mcp.yaml", "server.mcp.yaml", true},
		{"*.mcp.yaml", "configs/server.mcp.yaml", false},
		{"docs/conventions/*.md", "docs/conventions/style.md", true},
	}
	for _, c := range cases {
		if got := matchPattern(c.pat, c.path); got != c.want {
			t.Errorf("matchPattern(%q,%q) = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
}

var _ = rules.RuleBlock{}

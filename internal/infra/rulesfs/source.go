package rulesfs

import (
	"github.com/felixgeelhaar/tokenops/internal/contexts/rules"

	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultDiscoveryPatterns lists the glob patterns the Ingestor walks by
// default when no explicit override is supplied. Patterns are matched
// against the repo-relative path with forward slashes.
var DefaultDiscoveryPatterns = []string{
	"CLAUDE.md",
	"AGENTS.md",
	".cursor/rules/*",
	".cursor/rules/**/*",
	"*.mcp.yaml",
	"*.mcp.yml",
	"*.mcp.json",
	"docs/conventions/*.md",
}

// Ingestor is the infrastructure adapter that turns on-disk rule
// artifacts into in-memory rules.RuleDocument aggregates. It is the only piece
// of the rules domain that touches the filesystem; application services
// (LoadCorpus, Analyzer, Compressor, Router, Benchmark, ROIEngine,
// DetectConflicts) depend on the materialised []*rules.RuleDocument result,
// not the Ingestor itself.
//
// Tests substitute the FS field with an in-memory fs.FS; the production
// daemon uses Root + the OS file system. Both paths share the same
// glob/parsing/factory pipeline via rules.NewRuleDocument.
type Ingestor struct {
	// Root is the on-disk root the ingestor walks when FS is nil.
	Root string
	// FS, when set, replaces filesystem access. Used by tests.
	FS fs.FS
	// RepoID propagates to every emitted rules.RuleDocument and SourceID.
	RepoID string
	// Patterns overrides DefaultDiscoveryPatterns when non-empty.
	Patterns []string
	// MaxBytes caps the per-file body size; files larger than this are
	// skipped. Zero means 1 MiB.
	MaxBytes int64
}

// Discover returns the set of repo-relative paths that match the ingestor's
// patterns, sorted by path. Hidden directories (other than .cursor) and
// well-known vendor trees are skipped to keep the walk bounded.
func (in *Ingestor) Discover() ([]string, error) {
	patterns := in.Patterns
	if len(patterns) == 0 {
		patterns = DefaultDiscoveryPatterns
	}
	seen := map[string]struct{}{}
	visit := func(rel string, isDir bool) error {
		if isDir {
			base := filepath.Base(rel)
			if rel == "." || base == "." {
				return nil
			}
			if strings.HasPrefix(base, ".") && base != ".cursor" {
				return fs.SkipDir
			}
			if base == "node_modules" || base == "vendor" {
				return fs.SkipDir
			}
			return nil
		}
		for _, pat := range patterns {
			if matchPattern(pat, rel) {
				seen[rel] = struct{}{}
				return nil
			}
		}
		return nil
	}

	if in.FS != nil {
		if err := fs.WalkDir(in.FS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return visit(filepath.ToSlash(path), d.IsDir())
		}); err != nil {
			return nil, err
		}
	} else {
		root := in.Root
		if root == "" {
			root = "."
		}
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			return visit(filepath.ToSlash(rel), d.IsDir())
		}); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// Snapshot discovers, reads, and parses every matching rule artifact.
func (in *Ingestor) Snapshot() ([]*rules.RuleDocument, error) {
	paths, err := in.Discover()
	if err != nil {
		return nil, err
	}
	docs := make([]*rules.RuleDocument, 0, len(paths))
	for _, p := range paths {
		doc, err := in.LoadPath(p)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", p, err)
		}
		if doc == nil {
			continue
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// LoadPath reads a single artifact and returns a parsed rules.RuleDocument.
// Files exceeding the size cap are skipped (nil, nil).
func (in *Ingestor) LoadPath(path string) (*rules.RuleDocument, error) {
	max := in.MaxBytes
	if max <= 0 {
		max = 1 << 20
	}
	var body []byte
	var modTime time.Time
	if in.FS != nil {
		f, err := in.FS.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, nil
		}
		if info.Size() > max {
			return nil, nil
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		body = data
		modTime = info.ModTime()
	} else {
		full := filepath.Join(in.Root, path)
		info, err := os.Stat(full)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, nil
		}
		if info.Size() > max {
			return nil, nil
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		body = data
		modTime = info.ModTime()
	}
	src := rules.ClassifySource(path)
	doc, err := rules.NewRuleDocument(
		rules.MakeSourceID(in.RepoID, path),
		path,
		in.RepoID,
		string(body),
		src,
		rules.DefaultScope(src),
	)
	if err != nil {
		return nil, err
	}
	doc.ModTime = modTime
	return doc, nil
}

// matchPattern matches a path against a glob that supports "*" and "**".
// Patterns are evaluated against the forward-slash repo-relative path.
func matchPattern(pat, path string) bool {
	if pat == path {
		return true
	}
	if !strings.Contains(pat, "*") {
		return false
	}
	patSegs := strings.Split(pat, "/")
	pathSegs := strings.Split(path, "/")
	return matchSegments(patSegs, pathSegs)
}

func matchSegments(pat, path []string) bool {
	switch {
	case len(pat) == 0:
		return len(path) == 0
	case pat[0] == "**":
		for i := 0; i <= len(path); i++ {
			if matchSegments(pat[1:], path[i:]) {
				return true
			}
		}
		return false
	case len(path) == 0:
		return false
	default:
		ok, _ := filepath.Match(pat[0], path[0])
		if !ok {
			return false
		}
		return matchSegments(pat[1:], path[1:])
	}
}

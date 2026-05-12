package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// RuleBlock is the in-memory representation of one addressable section
// in a rule document. It carries the section body (kept local) plus
// its anchor path and a content hash. The body never enters event
// payloads.
//
// Immutability contract: once a RuleBlock has been added to a
// RuleDocument.Blocks slice and the document handed to any domain
// service (Analyzer, Compressor, Router, Conflicts), callers MUST NOT
// mutate the block. Use RuleDocument.BlocksCopy when a fresh slice is
// needed for adapter-side transforms.
type RuleBlock struct {
	// Anchor is the heading path, joined by "/", that addresses this block
	// within its parent document (e.g. "Testing/TDD").
	Anchor string
	// Level is the Markdown heading depth that introduced this block
	// (H1 = 1). Zero means the block is the preamble before any heading.
	Level int
	// Body is the raw text of the block, excluding the heading line. Kept
	// in-memory for analyzers and the compressor; never serialized to
	// events.
	Body string
}

// ID returns the stable identifier used for section-level analysis events.
// It is the combination of the parent document's SourceID and the block's
// anchor, joined by "#".
func (b RuleBlock) ID(sourceID string) string {
	anchor := b.Anchor
	if anchor == "" {
		anchor = "_preamble"
	}
	return sourceID + "#" + anchor
}

// CharCount returns the body's character length.
func (b RuleBlock) CharCount() int64 { return int64(len(b.Body)) }

// Hash returns the SHA-256 hex digest of the block body, prefixed with
// "sha256:".
func (b RuleBlock) Hash() string { return hashSHA256(b.Body) }

// RuleDocument is the in-memory representation of a rule artifact: a single
// CLAUDE.md, AGENTS.md, Cursor rules file, MCP policy file, or generic repo
// convention file.
type RuleDocument struct {
	// SourceID is a stable identifier derived from Path + RepoID.
	SourceID string
	// Source classifies the artifact kind.
	Source eventschema.RuleSource
	// Scope describes the operational granularity (defaults to repo).
	Scope eventschema.RuleScope
	// Path is the artifact path (absolute or repo-relative).
	Path string
	// RepoID, when set, is an opaque identifier for the originating
	// repository. Allows cross-repo aggregation without leaking repo names.
	RepoID string
	// Body is the artifact's raw text. Retained in the local rule store
	// only; never serialized to events.
	Body string
	// Blocks decomposes the body into addressable sections.
	Blocks []RuleBlock
	// ModTime is the source file modification time (when known).
	ModTime time.Time
}

// NewRuleDocument is the aggregate-root factory for RuleDocument. It is
// the single supported way to materialise a RuleDocument from raw text:
//   - SourceID, Path, and Source are required (returns an error when empty)
//   - Scope defaults to DefaultScope(Source) when zero
//   - Blocks are parsed from body via ParseMarkdown so callers cannot
//     supply mismatched body / block lists
//
// External callers (Ingestor, tests, future infrastructure adapters) go
// through this factory so RuleDocument invariants are enforced in one
// place — no struct-literal construction outside this package.
func NewRuleDocument(sourceID, path, repoID, body string, source RuleSourceKind, scope RuleScopeKind) (*RuleDocument, error) {
	if sourceID == "" {
		return nil, fmt.Errorf("rules: RuleDocument requires SourceID")
	}
	if path == "" {
		return nil, fmt.Errorf("rules: RuleDocument requires Path")
	}
	if source == "" {
		return nil, fmt.Errorf("rules: RuleDocument requires Source")
	}
	if scope == "" {
		scope = DefaultScope(source)
	}
	d := &RuleDocument{
		SourceID: sourceID,
		Source:   source,
		Scope:    scope,
		Path:     filepath.ToSlash(path),
		RepoID:   repoID,
		Body:     body,
	}
	d.Blocks = ParseMarkdown(body)
	return d, nil
}

// RuleSourceKind aliases the eventschema enum so callers can spell the
// factory parameter without importing the schema package; the
// underlying type is unchanged.
type RuleSourceKind = eventschema.RuleSource

// RuleScopeKind likewise aliases eventschema.RuleScope.
type RuleScopeKind = eventschema.RuleScope

// CharCount returns the document body's character length.
func (d *RuleDocument) CharCount() int64 { return int64(len(d.Body)) }

// BlocksCopy returns a defensive copy of the document's blocks so
// callers can iterate without risk of mutating the aggregate.
func (d *RuleDocument) BlocksCopy() []RuleBlock {
	if d == nil {
		return nil
	}
	out := make([]RuleBlock, len(d.Blocks))
	copy(out, d.Blocks)
	return out
}

// Hash returns the SHA-256 hex digest of the full document body, prefixed
// with "sha256:".
func (d *RuleDocument) Hash() string { return hashSHA256(d.Body) }

// ClassifySource maps a file path to its RuleSource taxonomy. Unknown
// extensions fall through to RuleSourceRepoConvention so callers can still
// ingest non-canonical instruction files.
func ClassifySource(path string) eventschema.RuleSource {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "claude.md":
		return eventschema.RuleSourceClaudeMD
	case "agents.md":
		return eventschema.RuleSourceAgentsMD
	}
	dir := strings.ToLower(filepath.ToSlash(filepath.Dir(path)))
	switch {
	case dir == ".cursor/rules",
		strings.HasSuffix(dir, "/.cursor/rules"),
		strings.HasPrefix(dir, ".cursor/rules/"),
		strings.Contains(dir, "/.cursor/rules/"):
		return eventschema.RuleSourceCursorRules
	case strings.HasSuffix(base, ".mcp.yaml"), strings.HasSuffix(base, ".mcp.yml"),
		strings.HasSuffix(base, ".mcp.json"), base == "mcp.policy.yaml":
		return eventschema.RuleSourceMCPPolicy
	}
	return eventschema.RuleSourceRepoConvention
}

// DefaultScope returns the scope to use when a rule source is observed
// without an explicit scope override. CLAUDE.md / AGENTS.md / repo
// conventions default to repo scope; Cursor rules with file globs default
// to file-glob scope; MCP policies default to tool scope.
func DefaultScope(src eventschema.RuleSource) eventschema.RuleScope {
	switch src {
	case eventschema.RuleSourceCursorRules:
		return eventschema.RuleScopeFileGlob
	case eventschema.RuleSourceMCPPolicy:
		return eventschema.RuleScopeTool
	default:
		return eventschema.RuleScopeRepo
	}
}

// MakeSourceID composes a stable identifier from a repository identifier
// and a repo-relative path. When repoID is empty the path is used as-is.
func MakeSourceID(repoID, path string) string {
	clean := filepath.ToSlash(path)
	if repoID == "" {
		return clean
	}
	return repoID + ":" + clean
}

func hashSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Package secgov enforces the security-suppression governance policy
// documented in security/SUPPRESSION-GOVERNANCE.md. Every entry in
// security/vex.json must carry a classification, a last_reviewed date,
// and a reviewer. Every scan.exclude entry in .nox.yaml must be
// preceded by a comment block documenting the same fields, and must
// still point at something real.
//
// These tests check what is cheap and always true: that a suppression
// is documented, and that it is not addressed to a path that no longer
// exists. They deliberately do NOT fail on review age. Age is a proxy
// for drift and a poor one — the 2026-09-14 review found suppressions
// that had been inert since a refactor months earlier, and a timer that
// blocks `go test ./...` makes a governance deadline look like a broken
// build while making a one-line date bump the cheapest way out. Staleness
// is reported by scripts/suppression-review-due.py, which warns without
// blocking. See the review log in SUPPRESSION-GOVERNANCE.md.
package secgov

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// validClassifications is the closed set from
// security/SUPPRESSION-GOVERNANCE.md. Any other label is a typo or an
// attempt to smuggle in an unsanctioned category.
var validClassifications = map[string]bool{
	"Real Issue":         true,
	"Acceptable Pattern": true,
	"False Positive":     true,
	"Deferred":           true,
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// archlint and this package run from internal/<pkg>; walk up two
	// directories to find the repo root rather than relying on GOWD.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

type vexGovernance struct {
	Classification string `json:"classification"`
	LastReviewed   string `json:"last_reviewed"`
	ReviewedBy     string `json:"reviewed_by"`
}

type vexStatement struct {
	Vulnerability   string         `json:"vulnerability"`
	Status          string         `json:"status"`
	Justification   string         `json:"justification"`
	ImpactStatement string         `json:"impact_statement"`
	Fingerprint     string         `json:"_nox_fingerprint"`
	Governance      *vexGovernance `json:"_governance"`
}

type vexDoc struct {
	Statements []vexStatement `json:"statements"`
}

func TestVEXStatementsHaveGovernanceMetadata(t *testing.T) {
	path := filepath.Join(repoRoot(t), "security", "vex.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// No waivers is a legitimate state, and the honest way to express
		// it: this test already refused an empty statements list, on the
		// grounds that a suppression list suppressing nothing reads as a
		// considered decision. Absence says the same thing without the
		// pretence. scripts/sec-gate.py treats a missing file as "no
		// waivers in effect" for the same reason.
		t.Skip("security/vex.json absent — no waivers to govern")
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc vexDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Statements) == 0 {
		t.Fatal("no VEX statements; remove the file or add waivers")
	}
	for i, s := range doc.Statements {
		tag := fmt.Sprintf("statement[%d] (%s/%s)", i, s.Vulnerability, shorten(s.Fingerprint))
		if s.Status != "not_affected" {
			t.Errorf("%s: status=%q want not_affected", tag, s.Status)
		}
		if strings.TrimSpace(s.ImpactStatement) == "" {
			t.Errorf("%s: missing impact_statement", tag)
		}
		if s.Fingerprint == "" {
			t.Errorf("%s: missing _nox_fingerprint", tag)
		}
		if s.Governance == nil {
			t.Errorf("%s: missing _governance block", tag)
			continue
		}
		if !validClassifications[s.Governance.Classification] {
			t.Errorf("%s: classification=%q not in {Real Issue, Acceptable Pattern, False Positive, Deferred}",
				tag, s.Governance.Classification)
		}
		if strings.TrimSpace(s.Governance.ReviewedBy) == "" {
			t.Errorf("%s: missing reviewed_by", tag)
		}
		if _, err := time.Parse("2006-01-02", s.Governance.LastReviewed); err != nil {
			t.Errorf("%s: last_reviewed=%q not YYYY-MM-DD: %v", tag, s.Governance.LastReviewed, err)
		}
	}
}

// noxExcludePattern detects a leaf path entry inside scan.exclude. We
// don't reach for a YAML parser because the governance contract lives
// in the comments preceding each entry — a structural parse drops
// those.
var (
	excludeEntryPattern = regexp.MustCompile(`^\s*-\s+["']?[^"'\n]+["']?\s*$`)
	classificationLine  = regexp.MustCompile(`#\s*Classification:\s*(.+)$`)
	reviewedLine        = regexp.MustCompile(`#\s*Last reviewed:\s*(\d{4}-\d{2}-\d{2})`)
	transientLine       = regexp.MustCompile(`#\s*Transient:`)
)

// excludeEntry is one scan.exclude path plus the governance metadata
// from the comment block that introduces its group.
type excludeEntry struct {
	path           string
	classification string
	reviewed       string
	transient      bool
}

// parseExcludes reads .nox.yaml and pairs every scan.exclude entry with
// the comment block above it. Scanning upward stops at a blank line or
// a non-comment, so each group owns its own metadata and a group that
// forgets it inherits nothing from the group before.
func parseExcludes(t *testing.T) []excludeEntry {
	t.Helper()
	path := filepath.Join(repoRoot(t), ".nox.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")

	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "exclude:") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatal("scan.exclude section not found in .nox.yaml")
	}

	var out []excludeEntry
	for i := start; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		// Stop at the next top-level key (e.g. `plugins:`).
		if trimmed != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(trimmed, "-") {
			break
		}
		if !excludeEntryPattern.MatchString(line) {
			continue
		}
		e := excludeEntry{path: strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), `"'`)}
		for j := i - 1; j >= start; j-- {
			c := strings.TrimSpace(lines[j])
			if c == "" {
				break
			}
			if excludeEntryPattern.MatchString(lines[j]) {
				continue
			}
			if !strings.HasPrefix(c, "#") {
				break
			}
			if m := classificationLine.FindStringSubmatch(c); m != nil && e.classification == "" {
				e.classification = strings.TrimSpace(m[1])
			}
			if m := reviewedLine.FindStringSubmatch(c); m != nil && e.reviewed == "" {
				e.reviewed = m[1]
			}
			if transientLine.MatchString(c) {
				e.transient = true
			}
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		t.Fatal("found scan.exclude section but no entries — parser regression?")
	}
	return out
}

func TestNoxExcludesHaveGovernanceComments(t *testing.T) {
	for _, e := range parseExcludes(t) {
		if e.classification == "" {
			t.Errorf("%s: missing `# Classification:` comment", e.path)
		} else if !validClassifications[e.classification] {
			t.Errorf("%s: classification=%q not in allowed set", e.path, e.classification)
		}
		if e.reviewed == "" {
			t.Errorf("%s: missing `# Last reviewed:` comment", e.path)
			continue
		}
		if _, err := time.Parse("2006-01-02", e.reviewed); err != nil {
			t.Errorf("%s: last_reviewed=%q not YYYY-MM-DD", e.path, e.reviewed)
		}
	}
}

// TestNoxExcludesResolveToSomething is the check that would have caught
// the 2026-09-14 findings on the day they appeared rather than four
// months later. Twelve of twenty-seven entries pointed at paths that had
// not existed since the DDD refactor moved those packages under
// internal/contexts/ — silently inert, while still reading as active
// policy in a security config.
//
// A suppression for a path that does not exist suppresses nothing. The
// exception is a generated artifact, which is legitimately absent from a
// clean checkout: those declare `# Transient:` in their comment block and
// say why, so the exemption is a written claim rather than a silent one.
func TestNoxExcludesResolveToSomething(t *testing.T) {
	tracked := trackedFiles(t)
	for _, e := range parseExcludes(t) {
		if e.transient {
			continue
		}
		if !resolve(tracked, e.path) {
			t.Errorf("%s: matches nothing git tracks — suppresses nothing.\n"+
				"\tEither repoint it at the path the file moved to, delete it, or\n"+
				"\tadd `# Transient: <why it is absent>` if it is a generated artifact.",
				e.path)
		}
	}
}

// trackedFiles is every path git has under version control, as a set.
//
// Tracking is the right question to ask, not existence on disk. Generated
// build output can be present on one developer's machine and absent on a
// clean checkout, so a filesystem-existence test would depend on local build
// state. What git tracks is the same for everyone.
func trackedFiles(t *testing.T) map[string]bool {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable (%v) — cannot check exclude liveness here", err)
	}
	set := make(map[string]bool)
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			set[p] = true
		}
	}
	if len(set) == 0 {
		t.Skip("git tracks no files here — not a checkout")
	}
	return set
}

// resolve reports whether one exclude entry addresses anything git
// tracks. nox patterns are not Go glob patterns: a trailing slash means
// a directory prefix and a leading `**/` means "at any depth", neither
// of which filepath.Match understands, so both are handled explicitly.
func resolve(tracked map[string]bool, pattern string) bool {
	if dir, ok := strings.CutSuffix(pattern, "/"); ok {
		for p := range tracked {
			if strings.HasPrefix(p, dir+"/") {
				return true
			}
		}
		return false
	}

	if base, ok := strings.CutPrefix(pattern, "**/"); ok {
		for p := range tracked {
			if filepath.Base(p) == base {
				return true
			}
		}
		return false
	}

	if strings.ContainsAny(pattern, "*?[") {
		for p := range tracked {
			if ok, _ := filepath.Match(pattern, p); ok {
				return true
			}
		}
		return false
	}

	return tracked[pattern]
}

func shorten(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

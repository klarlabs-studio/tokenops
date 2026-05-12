// Package secgov enforces the security-suppression governance policy
// documented in security/SUPPRESSION-GOVERNANCE.md. Every entry in
// security/vex.json must carry a classification, a last_reviewed date,
// and a reviewer. Every scan.exclude entry in .nox.yaml must be
// preceded by a comment block documenting the same fields. PRs that
// silently grow either list fail CI rather than rotting the audit
// trail.
package secgov

import (
	"encoding/json"
	"fmt"
	"os"
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

// reviewMaxAge is the staleness ceiling for any documented review.
// Matches the quarterly cadence in SUPPRESSION-GOVERNANCE.md plus a
// 30-day grace window so the test doesn't fail the day after a review.
const reviewMaxAge = 120 * 24 * time.Hour

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
	now := time.Now()
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
		ts, err := time.Parse("2006-01-02", s.Governance.LastReviewed)
		if err != nil {
			t.Errorf("%s: last_reviewed=%q not YYYY-MM-DD: %v", tag, s.Governance.LastReviewed, err)
			continue
		}
		if age := now.Sub(ts); age > reviewMaxAge {
			t.Errorf("%s: last_reviewed=%s is %d days old (max %d) — run the quarterly review",
				tag, s.Governance.LastReviewed, int(age.Hours()/24), int(reviewMaxAge.Hours()/24))
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
)

func TestNoxExcludesHaveGovernanceComments(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".nox.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")

	// Find the scan.exclude block and only inspect its entries.
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

	now := time.Now()
	entries := 0
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
		entries++
		// Scan upward (skipping sibling list entries) for the most
		// recent Classification / Last reviewed comments. The
		// governance contract says every exclude must have these in
		// the comment block immediately preceding the entry or its
		// sibling group.
		var classification, reviewed string
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
			if m := classificationLine.FindStringSubmatch(c); m != nil && classification == "" {
				classification = strings.TrimSpace(m[1])
			}
			if m := reviewedLine.FindStringSubmatch(c); m != nil && reviewed == "" {
				reviewed = m[1]
			}
		}
		entryTag := strings.TrimSpace(line)
		if classification == "" {
			t.Errorf("%s: missing `# Classification:` comment", entryTag)
		} else if !validClassifications[classification] {
			t.Errorf("%s: classification=%q not in allowed set", entryTag, classification)
		}
		if reviewed == "" {
			t.Errorf("%s: missing `# Last reviewed:` comment", entryTag)
			continue
		}
		ts, err := time.Parse("2006-01-02", reviewed)
		if err != nil {
			t.Errorf("%s: last_reviewed=%q not YYYY-MM-DD", entryTag, reviewed)
			continue
		}
		if age := now.Sub(ts); age > reviewMaxAge {
			t.Errorf("%s: last_reviewed=%s is %d days old (max %d)",
				entryTag, reviewed, int(age.Hours()/24), int(reviewMaxAge.Hours()/24))
		}
	}
	if entries == 0 {
		t.Fatal("found scan.exclude section but no entries — parser regression?")
	}
}

func shorten(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

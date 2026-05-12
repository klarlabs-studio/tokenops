package rules

import (
	"sort"
	"strings"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// FindingKind classifies a conflict finding.
type FindingKind string

// Known finding kinds.
const (
	// FindingRedundant marks two or more sections with identical bodies.
	// The rule corpus is paying for the same instructions in multiple
	// places.
	FindingRedundant FindingKind = "redundant"
	// FindingDrift marks the same anchor present in multiple rule sources
	// with diverging bodies (e.g., CLAUDE.md "Testing" vs AGENTS.md
	// "Testing"). Drift causes inconsistent agent behavior.
	FindingDrift FindingKind = "drift"
	// FindingAntiPattern marks sections whose bodies contain phrases from
	// known competing-incentive pairs (concise vs verbose, etc.).
	FindingAntiPattern FindingKind = "anti_pattern"
)

// Finding is a single conflict-detector hit.
type Finding struct {
	Kind FindingKind
	// Members lists section IDs (SourceID#Anchor) involved in the finding.
	Members []string
	// Anchor is the human-readable anchor when the finding is anchor-based.
	Anchor string
	// Detail is a short, human-readable explanation. Bodies are never
	// quoted in detail strings — only token counts and trigger phrases —
	// so the finding can be carried over OTLP without leaking text.
	Detail string
	// Triggers, when set, lists the trigger-phrase pairs that fired (one
	// pair per matched anti-pattern). Phrases are package-defined
	// constants, not user content.
	Triggers []string
}

// AntiPatternPair is a competing-incentive phrase pair. When two distinct
// sections (or two phrases inside one section) contain the two sides of a
// pair, the conflict detector emits an anti-pattern finding.
type AntiPatternPair struct {
	Name string
	A, B []string
}

// DefaultAntiPatterns is the seed list of competing-incentive phrase pairs.
// Phrases are matched case-insensitively, substring-style — the detector is
// deliberately coarse; downstream review is expected.
var DefaultAntiPatterns = []AntiPatternPair{
	{
		Name: "concise_vs_verbose",
		A:    []string{"be concise", "keep it short", "be brief", "terse"},
		B:    []string{"explain thoroughly", "be detailed", "verbose", "comprehensive explanation"},
	},
	{
		Name: "tdd_vs_test_after",
		A:    []string{"write tests first", "tdd", "red-green-refactor", "test before"},
		B:    []string{"write tests after", "tests after implementation", "skip tests"},
	},
	{
		Name: "comments_required_vs_no_comments",
		A:    []string{"always comment", "document every function", "comment all"},
		B:    []string{"no comments", "avoid comments", "comments are noise"},
	},
	{
		Name: "ask_before_acting_vs_autonomous",
		A:    []string{"ask before acting", "confirm with user", "wait for approval"},
		B:    []string{"act autonomously", "do not ask", "skip confirmation"},
	},
}

// ConflictOptions tunes the detector.
type ConflictOptions struct {
	// AntiPatterns overrides DefaultAntiPatterns when non-empty.
	AntiPatterns []AntiPatternPair
}

// DetectConflicts scans docs and returns conflict findings. The detector is
// pure and side-effect-free: it walks the in-memory RuleDocuments produced
// by the ingestor. No external services or LLM calls are involved.
func DetectConflicts(docs []*RuleDocument, opts ConflictOptions) []Finding {
	patterns := opts.AntiPatterns
	if len(patterns) == 0 {
		patterns = DefaultAntiPatterns
	}
	var findings []Finding

	// 1. Redundant: identical body hashes across sections.
	byHash := map[string][]string{}
	for _, d := range docs {
		for _, b := range d.Blocks {
			if strings.TrimSpace(b.Body) == "" {
				continue
			}
			h := b.Hash()
			byHash[h] = append(byHash[h], b.ID(d.SourceID))
		}
	}
	for h, ids := range byHash {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		findings = append(findings, Finding{
			Kind:    FindingRedundant,
			Members: ids,
			Detail:  "identical body across " + plural(len(ids), "section", "sections") + " (" + h + ")",
		})
	}

	// 2. Drift: same anchor in multiple sources, different hashes.
	type drift struct {
		anchor string
		ids    []string
		hashes map[string]bool
	}
	byAnchor := map[string]*drift{}
	for _, d := range docs {
		for _, b := range d.Blocks {
			if b.Anchor == "" {
				continue
			}
			key := strings.ToLower(b.Anchor)
			row, ok := byAnchor[key]
			if !ok {
				row = &drift{anchor: b.Anchor, hashes: map[string]bool{}}
				byAnchor[key] = row
			}
			row.ids = append(row.ids, b.ID(d.SourceID))
			row.hashes[b.Hash()] = true
		}
	}
	for _, row := range byAnchor {
		if len(row.ids) < 2 || len(row.hashes) < 2 {
			continue
		}
		sort.Strings(row.ids)
		findings = append(findings, Finding{
			Kind:    FindingDrift,
			Anchor:  row.anchor,
			Members: row.ids,
			Detail:  "anchor " + quote(row.anchor) + " diverges across " + plural(len(row.ids), "source", "sources"),
		})
	}

	// 3. Anti-patterns: trigger phrase pairs.
	type hit struct {
		id      string
		matched map[string]bool
	}
	hitsBySection := map[string]*hit{}
	noteHit := func(id, side string) {
		row, ok := hitsBySection[id]
		if !ok {
			row = &hit{id: id, matched: map[string]bool{}}
			hitsBySection[id] = row
		}
		row.matched[side] = true
	}
	for _, d := range docs {
		for _, b := range d.Blocks {
			body := strings.ToLower(b.Body)
			id := b.ID(d.SourceID)
			for _, pat := range patterns {
				if matchAny(body, pat.A) {
					noteHit(id, pat.Name+":A")
				}
				if matchAny(body, pat.B) {
					noteHit(id, pat.Name+":B")
				}
			}
		}
	}
	// Within-section: same section trips both sides of a pair.
	for _, h := range hitsBySection {
		for _, pat := range patterns {
			if h.matched[pat.Name+":A"] && h.matched[pat.Name+":B"] {
				findings = append(findings, Finding{
					Kind:     FindingAntiPattern,
					Members:  []string{h.id},
					Detail:   "section trips both sides of " + pat.Name,
					Triggers: []string{pat.Name},
				})
			}
		}
	}
	// Cross-section: distinct sections trip opposite sides of the same pair.
	for _, pat := range patterns {
		var sideA, sideB []string
		for _, h := range hitsBySection {
			if h.matched[pat.Name+":A"] && !h.matched[pat.Name+":B"] {
				sideA = append(sideA, h.id)
			}
			if h.matched[pat.Name+":B"] && !h.matched[pat.Name+":A"] {
				sideB = append(sideB, h.id)
			}
		}
		if len(sideA) == 0 || len(sideB) == 0 {
			continue
		}
		sort.Strings(sideA)
		sort.Strings(sideB)
		members := append([]string{}, sideA...)
		members = append(members, sideB...)
		findings = append(findings, Finding{
			Kind:     FindingAntiPattern,
			Members:  members,
			Detail:   "sections trip opposite sides of " + pat.Name,
			Triggers: []string{pat.Name},
		})
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}
		return strings.Join(findings[i].Members, ",") < strings.Join(findings[j].Members, ",")
	})
	return findings
}

// AsAnalysisEvent renders a finding as a RuleAnalysisEvent payload (one per
// finding) so conflicts flow through the same telemetry pipeline as ROI
// snapshots. Anchor and trigger metadata land in ConflictsWith /
// RedundantWith; no raw body content is carried.
func (f Finding) AsAnalysisEvent(sourceID string) *eventschema.RuleAnalysisEvent {
	ev := &eventschema.RuleAnalysisEvent{SourceID: sourceID}
	switch f.Kind {
	case FindingRedundant:
		ev.RedundantWith = append([]string{}, f.Members...)
	case FindingDrift, FindingAntiPattern:
		ev.ConflictsWith = append([]string{}, f.Members...)
	}
	return ev
}

func matchAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func plural(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return itoa(n) + " " + plural
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func quote(s string) string { return "\"" + s + "\"" }

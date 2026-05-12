package rules

import (
	"regexp"
	"strings"

	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// CompressConfig tunes the rule corpus compressor.
type CompressConfig struct {
	// SimilarityThreshold is the Jaccard score above which two sections
	// are treated as semantically redundant. 0 disables near-duplicate
	// pruning. Range 0.0–1.0. Default 0.85.
	SimilarityThreshold float64
	// MinSectionTokens skips compression for sections smaller than this
	// budget — they are usually one-line directives where compaction
	// changes meaning. Default 24.
	MinSectionTokens int64
	// ShingleSize is the word n-gram width used for Jaccard similarity.
	// Default 3.
	ShingleSize int
	// QualityFloor refuses to commit a compression result whose predicted
	// quality score (1 - compression_ratio_dropped) falls below this
	// threshold. Default 0.6.
	QualityFloor float64
}

func (c CompressConfig) withDefaults() CompressConfig {
	if c.SimilarityThreshold == 0 {
		c.SimilarityThreshold = 0.85
	}
	if c.MinSectionTokens == 0 {
		c.MinSectionTokens = 24
	}
	if c.ShingleSize == 0 {
		c.ShingleSize = 3
	}
	if c.QualityFloor == 0 {
		c.QualityFloor = 0.6
	}
	return c
}

// CompressedSection records the result of compressing a single section.
type CompressedSection struct {
	// SectionID is the source section identifier (SourceID#Anchor).
	SectionID string
	// Anchor is the source anchor.
	Anchor string
	// OriginalTokens is the section's token cost before compression.
	OriginalTokens int64
	// CompressedTokens is the section's token cost after compression.
	// Zero when Dropped is true.
	CompressedTokens int64
	// Body is the compressed body text. Empty when Dropped is true.
	Body string
	// Dropped reports that the section was removed (redundant or
	// near-duplicate of a kept section).
	Dropped bool
	// DroppedReason identifies why the section was removed
	// ("redundant" | "near_duplicate" | "").
	DroppedReason string
	// DuplicateOf, when Dropped is true, points at the SectionID the
	// dropped section was redundant with.
	DuplicateOf string
}

// CompressionResult bundles per-section outcomes and corpus-level totals.
type CompressionResult struct {
	SourceID         string
	Sections         []CompressedSection
	OriginalTokens   int64
	CompressedTokens int64
	QualityScore     float64
	// Accepted reflects the quality gate: when false, the compressor
	// recommends keeping the original corpus.
	Accepted bool
}

// Compressor distills rule corpora into a smaller behavioral representation
// by dropping redundant sections, pruning near-duplicates via Jaccard
// shingles, and compacting whitespace within retained sections.
type Compressor struct {
	cfg CompressConfig
	tok tokenizer.Tokenizer
}

// NewCompressor returns a Compressor for the given provider tokenizer. The
// tokenizer is used to convert byte-level reductions into token-level
// savings reported in CompressedSection.CompressedTokens.
func NewCompressor(cfg CompressConfig, tok tokenizer.Tokenizer) *Compressor {
	if tok == nil {
		tok = tokenizer.NewOpenAITokenizer()
	}
	return &Compressor{cfg: cfg.withDefaults(), tok: tok}
}

// Compress reduces doc into a CompressionResult. The original document is
// not mutated.
func (c *Compressor) Compress(doc *RuleDocument) *CompressionResult {
	if doc == nil {
		return nil
	}
	result := &CompressionResult{SourceID: doc.SourceID}
	keptShingles := map[string]map[string]struct{}{}
	hashKept := map[string]string{} // hash -> sectionID of first kept

	for _, b := range doc.Blocks {
		sectionID := b.ID(doc.SourceID)
		origTokens := int64(c.tok.CountText(b.Body))
		cs := CompressedSection{
			SectionID:      sectionID,
			Anchor:         b.Anchor,
			OriginalTokens: origTokens,
		}

		hash := normalizedHash(b.Body)
		if owner, dup := hashKept[hash]; dup {
			cs.Dropped = true
			cs.DroppedReason = "redundant"
			cs.DuplicateOf = owner
			result.Sections = append(result.Sections, cs)
			continue
		}

		var sh map[string]struct{}
		if c.cfg.SimilarityThreshold > 0 && origTokens >= c.cfg.MinSectionTokens {
			sh = shingleSet(b.Body, c.cfg.ShingleSize)
			for keptID, keptShingle := range keptShingles {
				if jaccard(sh, keptShingle) >= c.cfg.SimilarityThreshold {
					cs.Dropped = true
					cs.DroppedReason = "near_duplicate"
					cs.DuplicateOf = keptID
					break
				}
			}
		}
		if cs.Dropped {
			result.Sections = append(result.Sections, cs)
			continue
		}

		compacted := compactBody(b.Body)
		cs.Body = compacted
		cs.CompressedTokens = int64(c.tok.CountText(compacted))
		hashKept[hash] = sectionID
		if sh != nil {
			keptShingles[sectionID] = sh
		}
		result.Sections = append(result.Sections, cs)
	}

	for _, s := range result.Sections {
		result.OriginalTokens += s.OriginalTokens
		result.CompressedTokens += s.CompressedTokens
	}
	if result.OriginalTokens > 0 {
		result.QualityScore = float64(result.CompressedTokens) / float64(result.OriginalTokens)
	} else {
		result.QualityScore = 1.0
	}
	result.Accepted = result.QualityScore >= c.cfg.QualityFloor

	return result
}

// CompactedBody returns the corpus reassembled from the kept sections so
// callers can serialize the compressed form back to disk (or feed it into
// dynamic injection).
func (r *CompressionResult) CompactedBody() string {
	var b strings.Builder
	for _, s := range r.Sections {
		if s.Dropped {
			continue
		}
		if s.Anchor != "" {
			b.WriteString("## ")
			b.WriteString(s.Anchor)
			b.WriteByte('\n')
		}
		b.WriteString(s.Body)
		if !strings.HasSuffix(s.Body, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// AsAnalysisEvent renders the corpus-level result as a RuleAnalysisEvent
// with CompressedTokens populated; the compressor never persists raw text.
func (r *CompressionResult) AsAnalysisEvent() *eventschema.RuleAnalysisEvent {
	return &eventschema.RuleAnalysisEvent{
		SourceID:         r.SourceID,
		ContextTokens:    r.OriginalTokens,
		CompressedTokens: r.CompressedTokens,
	}
}

// --- helpers ---------------------------------------------------------

var (
	bulletRE = regexp.MustCompile(`(?m)^[\s]*[-*+]\s+`)
	wsRE     = regexp.MustCompile(`[ \t]+`)
	blankRE  = regexp.MustCompile(`\n{3,}`)
)

func compactBody(s string) string {
	out := wsRE.ReplaceAllString(s, " ")
	out = blankRE.ReplaceAllString(out, "\n\n")
	lines := strings.Split(out, "\n")
	dedup := make([]string, 0, len(lines))
	var lastNonBlank string
	lastBlank := true
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			if !lastBlank {
				dedup = append(dedup, "")
				lastBlank = true
			}
			continue
		}
		if t == lastNonBlank {
			continue
		}
		dedup = append(dedup, t)
		lastNonBlank = t
		lastBlank = false
	}
	return strings.TrimSpace(strings.Join(dedup, "\n"))
}

func normalizedHash(s string) string {
	out := strings.TrimSpace(s)
	out = wsRE.ReplaceAllString(out, " ")
	out = blankRE.ReplaceAllString(out, "\n\n")
	return hashSHA256(out)
}

func shingleSet(body string, n int) map[string]struct{} {
	words := splitWords(body)
	if len(words) < n {
		return nil
	}
	out := make(map[string]struct{}, len(words))
	for i := 0; i+n <= len(words); i++ {
		key := strings.ToLower(strings.Join(words[i:i+n], " "))
		out[key] = struct{}{}
	}
	return out
}

func splitWords(s string) []string {
	s = bulletRE.ReplaceAllString(s, "")
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ',', '.', ';', ':', '!', '?', '(', ')', '"', '\'':
			return true
		}
		return false
	})
	return fields
}

func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

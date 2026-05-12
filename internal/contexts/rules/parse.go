package rules

import (
	"bufio"
	"strings"
)

// ParseMarkdown splits a Markdown rule document into addressable blocks
// keyed by heading hierarchy. Lines starting with "#" delimit blocks; the
// preamble (content before any heading) is captured under an empty anchor.
//
// Code fences are respected: heading-like lines inside a fenced block are
// treated as content, not as a new section. Setext-style headings
// (underlined with ===/---) are not supported and will land in the
// preceding block.
//
// The returned blocks preserve their bodies (without the heading line) so
// the analyzer and compressor can operate on them; the public event
// representation derives only anchor + size + hash from these bodies.
func ParseMarkdown(body string) []RuleBlock {
	var blocks []RuleBlock
	stack := make([]string, 0, 4) // heading path

	current := &RuleBlock{Anchor: "", Level: 0}
	var b strings.Builder
	inFence := false
	fence := ""

	flush := func() {
		current.Body = b.String()
		blocks = append(blocks, *current)
	}

	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimLeft(line, " \t")

		// Track code fences (``` or ~~~) so headings inside them stay
		// content.
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			marker := trim[:3]
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trim, fence) {
				inFence = false
				fence = ""
			}
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}

		if !inFence && len(trim) > 0 && trim[0] == '#' {
			level, title := splitHeading(trim)
			if level > 0 {
				flush()
				b.Reset()
				stack = adjustStack(stack, level, title)
				current = &RuleBlock{
					Anchor: strings.Join(stack, "/"),
					Level:  level,
				}
				continue
			}
		}

		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	return blocks
}

// splitHeading parses a Markdown ATX heading. Returns (0, "") for lines
// that begin with # but are not valid headings (e.g. "#####tag").
func splitHeading(s string) (int, string) {
	level := 0
	for level < len(s) && level < 6 && s[level] == '#' {
		level++
	}
	if level == 0 || level >= len(s) {
		return 0, ""
	}
	if s[level] != ' ' && s[level] != '\t' {
		return 0, ""
	}
	title := strings.TrimSpace(s[level+1:])
	return level, title
}

// adjustStack updates the heading path so the new heading of level is
// appended after popping deeper or equal-level ancestors.
func adjustStack(stack []string, level int, title string) []string {
	for len(stack) >= level {
		stack = stack[:len(stack)-1]
	}
	for len(stack) < level-1 {
		stack = append(stack, "")
	}
	return append(stack, title)
}

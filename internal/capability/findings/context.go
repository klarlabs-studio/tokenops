package findings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/prefixlint"
)

// KindContext is the agent's always-loaded context.
const KindContext = "context"

// volatileContext says the context every session loads carries something
// that changes each session — a SessionStart hook that prints the date —
// and so starts every session with a cold cache from there. Only
// high-severity items make a finding: a date written in CLAUDE.md
// changes when the file is edited, and stays in `tokenops coach
// lint-context`.
func volatileContext(r *prefixlint.Report) []Finding {
	if r == nil {
		return nil
	}
	high := r.HighFindings()
	if len(high) == 0 {
		return nil
	}
	title := fmt.Sprintf("%d things in every session's context change each session, so its cache is rebuilt", len(high))
	if len(high) == 1 {
		title = "A " + high[0].Subject() + " changes every session's context, so its cache is rebuilt"
	}
	var where []string
	for i, f := range high {
		if i == 3 {
			where = append(where, fmt.Sprintf("and %d more", len(high)-3))
			break
		}
		where = append(where, f.Subject()+" ("+site(f)+")")
	}
	return []Finding{{
		Key: key(KindContext, "volatile-prefix"), Kind: KindContext, Level: LevelNotice,
		Title:    title,
		Evidence: strings.Join(where, "; "),
		Action:   high[0].Fix + " `tokenops coach lint-context` lists each item with its fix.",
	}}
}

// site is where an item is, with the home directory as ~.
func site(f prefixlint.Finding) string {
	p := f.Path
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = filepath.Join("~", rel)
		}
	}
	if p == "" {
		return "recorded in transcripts"
	}
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d", p, f.Line)
	}
	return p
}

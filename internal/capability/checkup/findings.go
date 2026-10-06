package checkup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// bySession groups records per session, oldest first.
func bySession(records []agentdx.Record) [][]agentdx.Record {
	m := map[string][]agentdx.Record{}
	for _, r := range records {
		m[r.SessionID] = append(m[r.SessionID], r)
	}
	out := make([][]agentdx.Record, 0, len(m))
	for _, rs := range m {
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
		out = append(out, rs)
	}
	return out
}

// readTools and writeTools name file reads and writes across clients.
var (
	readTools  = map[string]bool{"read": true, "view": true, "read_file": true, "readfile": true}
	writeTools = map[string]bool{
		"edit": true, "multiedit": true, "write": true, "apply_patch": true,
		"str_replace_editor": true, "write_file": true, "notebookedit": true,
	}
)

// bytesPerToken is the usual estimate for source text.
const bytesPerToken = 4

// rereads counts reads of a file already read in the same session with no
// write to it in between: the agent paying again for what it has. The
// read guard refuses exactly these.
func rereads(records []agentdx.Record, guardOn bool) []Finding {
	var count, sessionsHit int
	var tokens int64
	files := map[string]bool{}
	for _, rs := range bySession(records) {
		read := map[string]bool{}
		hit := false
		for _, r := range rs {
			if r.Kind != agentdx.KindToolUse || r.FilePath == "" {
				continue
			}
			tool := strings.ToLower(r.ToolName)
			switch {
			case writeTools[tool]:
				delete(read, r.FilePath)
			case readTools[tool] && read[r.FilePath]:
				count++
				hit = true
				files[r.FilePath] = true
				tokens += fileTokens(r.FilePath)
			case readTools[tool]:
				read[r.FilePath] = true
			}
		}
		if hit {
			sessionsHit++
		}
	}
	if count == 0 {
		return nil
	}
	level := LevelNotice
	if count >= 50 {
		level = LevelWarn
	}
	evidence := fmt.Sprintf("%d re-reads of %d files across %d sessions, with no edit in between", count, len(files), sessionsHit)
	if tokens > 0 {
		evidence += fmt.Sprintf(" — about %s tokens read again", humanTokens(tokens))
	}
	f := Finding{
		Kind: "rereads", Level: level,
		Title:    fmt.Sprintf("Agents re-read %d unchanged files", len(files)),
		Evidence: evidence,
		Fix:      "tokenops hooks install --read-guard   (refuses a read of a file already in context)",
	}
	if guardOn {
		// These got past a guard already on: in Codex or Cursor, which
		// cannot refuse a read, or at a mode that only observes.
		f.Level = LevelInfo
		f.Fix = "the read guard is on; `tokenops coach stats` shows what it refused; these passed it (Codex and Cursor cannot refuse a read)"
	}
	return []Finding{f}
}

// fileTokens estimates a file's size in tokens from its size today.
func fileTokens(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size() / bytesPerToken
}

// ruleFiles are the instruction files a client loads into every session.
var ruleFiles = []string{"CLAUDE.md", "AGENTS.md", filepath.Join(".claude", "CLAUDE.md"), "GEMINI.md"}

// standingContext is what instruction files cost: every turn re-reads the
// whole context, so a file loaded at the start of a session is paid for
// on every turn after.
func standingContext(home string, turnsByDir map[string]int, all Usage) []Finding {
	global := fileTokens(filepath.Join(home, ".claude", "CLAUDE.md"))
	var weighted int64
	turns := 0
	for dir, n := range turnsByDir {
		weighted += (global + dirRuleTokens(dir, home)) * int64(n)
		turns += n
	}
	if weighted == 0 || all.InputTokens == 0 {
		return nil
	}
	share := float64(weighted) / float64(all.InputTokens) * 100
	level := LevelInfo
	if share >= 5 {
		level = LevelNotice
	}
	evidence := fmt.Sprintf("Instruction files were re-read on %d turns: %s tokens, %.1f%% of all input", turns, humanTokens(weighted), share)
	if global > 0 {
		evidence += fmt.Sprintf("; your global ~/.claude/CLAUDE.md alone is %s tokens on every Claude Code turn", humanTokens(global))
	}
	return []Finding{{
		Kind: "standing_context", Level: level,
		Title:    fmt.Sprintf("Standing instructions are %.0f%% of what your agents read", share),
		Evidence: evidence,
		Fix:      "tokenops rules analyze   (then `tokenops rules compress` for the duplicates it finds)",
	}}
}

// dirRuleTokens sums the instruction files a session in dir loads: dir's
// own and each parent's up to home, as Claude Code does.
func dirRuleTokens(dir, home string) int64 {
	var n int64
	global := filepath.Join(home, ".claude", "CLAUDE.md")
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		for _, f := range ruleFiles {
			// The global file is counted once, by the caller.
			if p := filepath.Join(d, f); p != global {
				n += fileTokens(p)
			}
		}
		if d == home || d == filepath.Dir(d) || !strings.HasPrefix(d, home) {
			return n
		}
	}
}

// oversized counts lookup instructions — find it, show it, list it —
// answered on a flagship model. A cheaper model closes those just as
// well, and the route guard suggests one as the instruction arrives.
func oversized(records []agentdx.Record, guardOn bool) []Finding {
	catalog := modeltier.New(spend.DefaultTable(), nil)
	var lookups, onDeep int
	models := map[string]int{}
	for _, rs := range bySession(records) {
		kind := taskclass.KindUnknown
		pending := false
		for _, r := range rs {
			switch {
			case r.Kind == agentdx.KindPrompt:
				kind = taskclass.KindForTurn(r.Text, kind)
				pending = kind == taskclass.KindLookup
			case r.Kind == agentdx.KindAssistantTurn && pending && r.Model != "":
				pending = false
				lookups++
				if catalog.Resolve(eventschema.Provider(r.Provider), r.Model).Tier == modeltier.TierDeep {
					onDeep++
					models[r.Model]++
				}
			}
		}
	}
	if onDeep == 0 || lookups < 10 {
		return nil
	}
	share := float64(onDeep) / float64(lookups) * 100
	level := LevelInfo
	if share >= 50 {
		level = LevelNotice
	}
	f := Finding{
		Kind: "oversized_model", Level: level,
		Title:    fmt.Sprintf("%d of %d lookups ran on a flagship model", onDeep, lookups),
		Evidence: fmt.Sprintf("Instructions that only find, show or list something ran on %s (%.0f%%)", topModels(models), share),
		Fix:      "tokenops hooks install --route-guard   (suggests a cheaper model as a lookup arrives)",
	}
	if guardOn {
		f.Fix = "the route guard is on and suggests a cheaper model; `tokenops coach set models autonomous` lets it switch"
	}
	return []Finding{f}
}

func topModels(m map[string]int) string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return m[names[i]] > m[names[j]] })
	if len(names) > 2 {
		names = names[:2]
	}
	return strings.Join(names, ", ")
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

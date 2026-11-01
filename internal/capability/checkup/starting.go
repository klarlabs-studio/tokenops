package checkup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/claudehome"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// minStartingSessions is how many sessions a starting-context figure needs
// before it says anything about how sessions start.
const minStartingSessions = 5

// startingNotice is the starting context, in tokens, from which it is
// worth acting on: every turn of every session reads it again.
const startingNotice = 40_000

// startingContext is what a Claude Code session carries before the
// operator's first word: its system prompt, the tool definitions of every
// MCP server and skill configured, and its instruction files, measured as
// the context of each session's first turn. A subagent starts a context of
// its own and is left out.
func startingContext(records []agentdx.Record, home string) []Finding {
	var firsts []float64
	for _, rs := range bySession(records) {
		if first, ok := freshStart(rs); ok {
			firsts = append(firsts, float64(first))
		}
	}
	if len(firsts) < minStartingSessions {
		return nil
	}
	sort.Float64s(firsts)
	// The median, not a tail: a long first instruction (a pasted file, a
	// continued session's summary) is the operator's, not the harness's.
	median := int64(firsts[len(firsts)/2])
	level := LevelInfo
	if median >= startingNotice {
		level = LevelNotice
	}
	servers, skills := mcpServers(home), skillCount(home)
	evidence := fmt.Sprintf("The first turn of %d sessions carried a median %s tokens before any work: Claude Code's system prompt, the tools of %d MCP servers and %d skills configured, instruction files and your first instruction",
		len(firsts), humanTokens(median), servers, skills)
	if g := fileTokens(filepath.Join(claudehome.Resolve(os.Getenv, home), "CLAUDE.md")); g > 0 {
		evidence += fmt.Sprintf(" (your global CLAUDE.md is %s of it)", humanTokens(g))
	}
	evidence += ". Every turn reads it again."
	return []Finding{{
		Kind: "starting_context", Level: level,
		Title:    fmt.Sprintf("Sessions start with %s tokens of context before your first word", humanTokens(median)),
		Evidence: evidence,
		Fix:      "remove MCP servers you rarely use (`claude mcp remove <name>`) or move them into the projects that need them (.mcp.json), and keep only the skills you use",
	}}
}

// freshStart is the context of a session's first turn, when the session
// started fresh: a Claude Code session of the operator's (not a
// subagent's) whose first entry is their instruction. A resumed or
// compacted session's first turn carries the conversation so far.
func freshStart(rs []agentdx.Record) (int64, bool) {
	if len(rs) == 0 || rs[0].Harness != agentdx.SourceClaudeCode || strings.Contains(rs[0].SessionID, "/") || rs[0].Kind != agentdx.KindPrompt {
		return 0, false
	}
	for _, r := range rs[1:] {
		switch {
		case r.Kind == agentdx.KindCompaction:
			return 0, false
		case r.Kind == agentdx.KindAssistantTurn && r.InputTokens > 0:
			return r.InputTokens, true
		}
	}
	return 0, false
}

// mcpServers counts the MCP servers configured for every Claude Code
// session, in ~/.claude.json.
func mcpServers(home string) int {
	b, err := os.ReadFile(filepath.Join(home, ".claude.json")) //nolint:gosec // the operator's own Claude Code config
	if err != nil {
		return 0
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return 0
	}
	return len(cfg.MCPServers)
}

// skillCount counts the skills in the operator's own skills directory.
func skillCount(home string) int {
	entries, err := os.ReadDir(filepath.Join(claudehome.Resolve(os.Getenv, home), "skills"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	return n
}

package checkup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/claudehome"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
)

// unusedNotice is the unused loadout, in tokens a turn, from which the
// finding is worth acting on rather than noting.
const unusedNotice = 2_000

// unusedShown is how many unused owners the finding names.
const unusedShown = 4

// owner is what puts something into every turn's context: a plugin (its
// skills and MCP servers), a claude.ai connector, or an MCP server.
type owner struct {
	kind, name string
	// tokens is its largest size in a session that loaded it.
	tokens int64
	// tokenTurns is its size times the turns of each session that loaded
	// it: what it was read for in the window.
	tokenTurns int64
	sessions   int
	uses       int
}

// loadout is what one session loaded, by owner key, in bytes, and what it
// used.
type loadout struct {
	skills  map[string]map[string]int // plugin → skill line → bytes
	tools   map[string]int            // deferred tool name → bytes
	notes   map[string]int            // MCP server → instruction bytes
	uses    map[string]int            // owner key → uses
	turns   map[string]bool           // assistant message ids in the window
	present bool
}

// pluginSkill is a plugin skill's entry in the skill listing:
// "- plugin:skill: description" or "- plugin:skill".
var pluginSkill = regexp.MustCompile(`^- ([\w.-]+):([\w.-]+)(?::|$)`)

// transcriptEntry is the part of a Claude Code transcript line the
// finding reads. Content is only measured, never kept.
type transcriptEntry struct {
	Type       string    `json:"type"`
	Timestamp  time.Time `json:"timestamp"`
	Attachment *struct {
		Type         string   `json:"type"`
		Content      string   `json:"content"`
		AddedNames   []string `json:"addedNames"`
		RemovedNames []string `json:"removedNames"`
		AddedBlocks  []string `json:"addedBlocks"`
	} `json:"attachment"`
	Message *struct {
		ID      string          `json:"id"`
		Usage   json.RawMessage `json:"usage"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// unusedContext names what Claude Code loaded into every turn of the
// window's sessions and never used there: plugins whose skills were never
// invoked and whose tools were never called, claude.ai connectors and MCP
// servers never called. Skills of the operator's own and Claude Code's
// built-in ones are left out: the listing does not say which are which.
func unusedContext(ctx context.Context, home string, since time.Time) []Finding {
	root, err := claudecodejsonl.DefaultRoot()
	owners := readLoadouts(ctx, root, err, since)
	return unusedFinding(owners, claudehome.Resolve(os.Getenv, home))
}

func unusedFinding(owners map[string]*owner, claudeDir string) []Finding {
	off := disabledPlugins(claudeDir)
	var unused []*owner
	for _, o := range owners {
		// A plugin turned off since still sits in the window's sessions.
		if o.kind == "plugin" && off[pluginID(o.name, claudeDir)] {
			continue
		}
		if o.uses == 0 && o.sessions >= minStartingSessions && o.tokens > 0 {
			unused = append(unused, o)
		}
	}
	if len(unused) == 0 {
		return nil
	}
	sort.Slice(unused, func(i, j int) bool {
		if unused[i].tokenTurns != unused[j].tokenTurns {
			return unused[i].tokenTurns > unused[j].tokenTurns
		}
		return unused[i].kind+unused[i].name < unused[j].kind+unused[j].name
	})
	var perTurn, read int64
	for _, o := range unused {
		perTurn += o.tokens
		read += o.tokenTurns
	}
	named := make([]string, 0, unusedShown)
	fixes := make([]string, 0, unusedShown)
	for _, o := range unused[:min(len(unused), unusedShown)] {
		named = append(named, fmt.Sprintf("%s %s (%s tokens, %d sessions)", o.kind, o.name, humanTokens(o.tokens), o.sessions))
		fixes = append(fixes, fixFor(o, claudeDir))
	}
	evidence := fmt.Sprintf("%d loaded into every turn and never used: %s", len(unused), strings.Join(named, ", "))
	if len(unused) > unusedShown {
		evidence += fmt.Sprintf(" and %d more", len(unused)-unusedShown)
	}
	evidence += fmt.Sprintf(". Together %s tokens a turn, %s tokens read in the window", humanTokens(perTurn), humanTokens(read))
	level := LevelInfo
	if perTurn >= unusedNotice {
		level = LevelNotice
	}
	return []Finding{{
		Kind: "unused_context", Level: level,
		Title:    fmt.Sprintf("%s tokens a turn load tools and skills you never used", humanTokens(perTurn)),
		Evidence: evidence,
		Fix:      strings.Join(fixes, "; "),
	}}
}

// fixFor is the command that unloads o. A plugin is named as Claude Code
// knows it: name@marketplace when installed, name@synced when it comes
// from claude.ai.
func fixFor(o *owner, claudeDir string) string {
	switch o.kind {
	case "plugin":
		return fmt.Sprintf("`claude plugin disable %s`", pluginID(o.name, claudeDir))
	case "connector":
		return fmt.Sprintf("turn the %s connector off on claude.ai", o.name)
	}
	return fmt.Sprintf("`claude mcp remove %s` (or move it to the projects that use it)", o.name)
}

// disabledPlugins are the plugins turned off in the user's settings.
func disabledPlugins(claudeDir string) map[string]bool {
	off := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(claudeDir, "settings.json")) //nolint:gosec // the operator's own Claude Code config
	if err != nil {
		return off
	}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if json.Unmarshal(b, &settings) != nil {
		return off
	}
	for id, on := range settings.EnabledPlugins {
		if !on {
			off[id] = true
		}
	}
	return off
}

// pluginID resolves a plugin's name to the id `claude plugin` takes.
func pluginID(name, claudeDir string) string {
	if b, err := os.ReadFile(filepath.Join(claudeDir, "plugins", "installed_plugins.json")); err == nil { //nolint:gosec // the operator's own Claude Code config
		var installed struct {
			Plugins map[string]json.RawMessage `json:"plugins"`
		}
		if json.Unmarshal(b, &installed) == nil {
			for id := range installed.Plugins {
				if strings.HasPrefix(id, name+"@") {
					return id
				}
			}
		}
	}
	manifests, _ := filepath.Glob(filepath.Join(claudeDir, "plugins", "synced", "*", "manifest.json"))
	for _, m := range manifests {
		b, err := os.ReadFile(m) //nolint:gosec // the operator's own Claude Code config
		if err != nil {
			continue
		}
		var synced struct {
			Plugins []struct {
				Name string `json:"name"`
			} `json:"plugins"`
		}
		if json.Unmarshal(b, &synced) != nil {
			continue
		}
		for _, p := range synced.Plugins {
			if p.Name == name {
				return name + "@synced"
			}
		}
	}
	return name
}

// ownerOf is the owner of an MCP server, by the server part of its tool
// names: a plugin's server is plugin_<plugin>_<server>, a claude.ai
// connector's claude_ai_<name>.
func ownerOf(server string) (kind, name string) {
	switch {
	case strings.HasPrefix(server, "plugin_"):
		rest := strings.TrimPrefix(server, "plugin_")
		if i := strings.Index(rest, "_"); i > 0 {
			return "plugin", rest[:i]
		}
		return "plugin", rest
	case strings.HasPrefix(server, "claude_ai_"):
		return "connector", strings.ReplaceAll(strings.TrimPrefix(server, "claude_ai_"), "_", " ")
	}
	return "MCP server", server
}

// serverKey turns an MCP server's display name ("claude.ai Gmail",
// "plugin:data:hex") into the server part of its tool names.
func serverKey(display string) string {
	return strings.NewReplacer(".", "_", " ", "_", ":", "_").Replace(display)
}

// toolServer is the server part of an MCP tool name, mcp__<server>__<tool>.
func toolServer(tool string) (string, bool) {
	parts := strings.SplitN(tool, "__", 3)
	if len(parts) != 3 || parts[0] != "mcp" {
		return "", false
	}
	return parts[1], true
}

// readLoadouts reads every Claude Code session changed since since, the
// operator's own (not subagents', which carry no listing of their own).
func readLoadouts(ctx context.Context, root string, rootErr error, since time.Time) map[string]*owner {
	files, err := sessionFiles("Claude Code", root, rootErr, claudecodejsonl.FindSessionFiles)
	owners := map[string]*owner{}
	if err != nil {
		return owners
	}
	for _, f := range recentFiles(files, since) {
		if ctx.Err() != nil {
			break
		}
		if strings.Contains(filepath.ToSlash(f), "/subagents/") {
			continue
		}
		fh, err := os.Open(f) //nolint:gosec // the operator's own transcripts
		if err != nil {
			continue
		}
		l := readLoadout(fh, since)
		_ = fh.Close()
		fold(owners, l)
	}
	return owners
}

// readLoadout reads one session's transcript.
func readLoadout(r io.Reader, since time.Time) loadout {
	l := loadout{skills: map[string]map[string]int{}, tools: map[string]int{}, notes: map[string]int{},
		uses: map[string]int{}, turns: map[string]bool{}}
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && (bytes.Contains(line, []byte(`"attachment"`)) || bytes.Contains(line, []byte(`"assistant"`))) {
			l.line(line, since)
		}
		if err != nil {
			return l
		}
	}
}

func (l *loadout) line(raw []byte, since time.Time) {
	var e transcriptEntry
	if json.Unmarshal(raw, &e) != nil {
		return
	}
	if a := e.Attachment; e.Type == "attachment" && a != nil {
		switch a.Type {
		case "skill_listing":
			l.present = true
			l.skills = map[string]map[string]int{}
			for _, s := range strings.Split(a.Content, "\n") {
				if m := pluginSkill.FindStringSubmatch(s); m != nil {
					if l.skills[m[1]] == nil {
						l.skills[m[1]] = map[string]int{}
					}
					l.skills[m[1]][m[2]] = len(s) + 1
				}
			}
		case "deferred_tools_delta":
			l.present = true
			for _, n := range a.AddedNames {
				l.tools[n] = len(n) + 1
			}
			for _, n := range a.RemovedNames {
				delete(l.tools, n)
			}
		case "mcp_instructions_delta":
			l.present = true
			for i, n := range a.AddedNames {
				if i < len(a.AddedBlocks) {
					l.notes[serverKey(n)] = len(a.AddedBlocks[i])
				}
			}
			for _, n := range a.RemovedNames {
				delete(l.notes, serverKey(n))
			}
		}
		return
	}
	if e.Type != "assistant" || e.Message == nil {
		return
	}
	if len(e.Message.Usage) > 0 && e.Message.ID != "" && !e.Timestamp.Before(since) {
		l.turns[e.Message.ID] = true
	}
	var blocks []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Input struct {
			Skill string `json:"skill"`
		} `json:"input"`
	}
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		if server, ok := toolServer(b.Name); ok {
			kind, name := ownerOf(server)
			l.uses[kind+"\x00"+name]++
		} else if b.Name == "Skill" {
			if plugin, _, ok := strings.Cut(b.Input.Skill, ":"); ok {
				l.uses["plugin\x00"+plugin]++
			}
		}
	}
}

// fold adds one session's loadout to the owners.
func fold(owners map[string]*owner, l loadout) {
	if !l.present || len(l.turns) == 0 {
		return
	}
	size := map[string]int{}
	for plugin, skills := range l.skills {
		for _, b := range skills {
			size["plugin\x00"+plugin] += b
		}
	}
	for tool, b := range l.tools {
		if server, ok := toolServer(tool); ok {
			kind, name := ownerOf(server)
			size[kind+"\x00"+name] += b
		}
	}
	for server, b := range l.notes {
		kind, name := ownerOf(server)
		size[kind+"\x00"+name] += b
	}
	for key, b := range size {
		o := owners[key]
		if o == nil {
			kind, name, _ := strings.Cut(key, "\x00")
			o = &owner{kind: kind, name: name}
			owners[key] = o
		}
		tokens := int64(b / bytesPerToken)
		o.tokens = max(o.tokens, tokens)
		o.tokenTurns += tokens * int64(len(l.turns))
		o.sessions++
	}
	for key, n := range l.uses {
		if o := owners[key]; o != nil {
			o.uses += n
		} else {
			kind, name, _ := strings.Cut(key, "\x00")
			owners[key] = &owner{kind: kind, name: name, uses: n}
		}
	}
}

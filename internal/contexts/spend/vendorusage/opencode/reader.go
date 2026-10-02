// Package opencode reads opencode's local session store — a SQLite database
// at ~/.local/share/opencode/opencode.db — and surfaces per-assistant-turn
// token usage as TokenOps PromptEvents. Unlike the Claude Code and Codex
// readers (which parse JSONL files), opencode persists sessions in SQLite;
// this reader opens that database read-only so it never contends with a
// running opencode process.
package opencode

import (
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Turn is one assistant message extracted from the opencode message table.
type Turn struct {
	ID        string // message primary key — the dedup + deterministic-id source
	SessionID string
	Project   string // derived from the session's working directory
	Model     string // opencode modelID (e.g. "claude-opus-4.6")
	Provider  eventschema.Provider
	// Endpoint names the endpoint the turn went through when the
	// providerID says it (a coding plan's own, or a vendor's pay-as-you-go
	// API); empty when it does not. A plan covers only its own (ADR 0009).
	Endpoint     string
	InputTokens  int // uncached input + cache read + cache write
	CachedTokens int // cache read
	OutputTokens int // output + reasoning
	Cost         float64
	Timestamp    time.Time
}

// DefaultRoot returns opencode's database path: OPENCODE_DB, then
// XDG_DATA_HOME, then ~/.local/share (opencodedb.DefaultPath).
func DefaultRoot() (string, error) { return opencodedb.DefaultPath() }

// ReadSession is ReadMessages scoped to one session.
//
// The coaching hook runs when a session goes idle and needs only that
// session's turns. Scanning all of them would mean a full pass over a
// store that reaches 1.2 GB here, on a path that fires every time the
// operator stops typing.
func ReadSession(dbPath, sessionID string, visit func(Turn) error) error {
	if sessionID == "" {
		return nil
	}
	return readMessages(dbPath, sessionID, visit)
}

// ReadMessages opens dbPath read-only and invokes visit for every assistant
// turn that carries token usage, from opencode 1.x and 2.x alike. A missing
// database is not an error (opencode may not be installed).
func ReadMessages(dbPath string, visit func(Turn) error) error {
	return readMessages(dbPath, "", visit)
}

func readMessages(dbPath, sessionID string, visit func(Turn) error) error {
	return opencodedb.Read(dbPath, opencodedb.Options{SessionID: sessionID}, func(m opencodedb.Message) error {
		if m.Role != opencodedb.Assistant {
			return nil
		}
		t := m.Tokens
		if t.Input+t.Output+t.Reasoning+t.CacheRead+t.CacheWrite == 0 {
			return nil
		}
		return visit(Turn{
			ID:           m.ID,
			SessionID:    m.SessionID,
			Project:      projectFromPath(m.Root, m.CWD),
			Model:        m.ModelID,
			Provider:     mapProvider(m.ProviderID),
			Endpoint:     endpointFor(m.ProviderID),
			InputTokens:  int(t.Input + t.CacheRead + t.CacheWrite),
			CachedTokens: int(t.CacheRead),
			OutputTokens: int(t.Output + t.Reasoning),
			Cost:         m.Cost,
			Timestamp:    m.Created,
		})
	})
}

// projectFromPath derives a stable project label from the session's working
// directory: the git root's basename when it is a real path, otherwise the
// cwd's basename.
func projectFromPath(root, cwd string) string {
	if root != "" && root != "/" {
		return filepath.Base(root)
	}
	if cwd != "" {
		return filepath.Base(cwd)
	}
	return "unknown"
}

// opencodeProviders maps opencode's providerID (models.dev's provider
// IDs) to the TokenOps provider that bills it and the endpoint it names.
// A vendor that sells both a coding plan and a pay-as-you-go API has an ID
// for each: the plan's endpoint is named like the provider, the API's
// "<provider>-api", so a bound plan covers only the plan's turns.
var opencodeProviders = map[string]struct {
	provider eventschema.Provider
	endpoint string
}{
	"anthropic":              {eventschema.ProviderAnthropic, ""},
	"openai":                 {eventschema.ProviderOpenAI, ""},
	"github-copilot":         {eventschema.ProviderGitHub, ""},
	"github":                 {eventschema.ProviderGitHub, ""},
	"google":                 {eventschema.ProviderGemini, ""},
	"gemini":                 {eventschema.ProviderGemini, ""},
	"google-vertex":          {eventschema.ProviderGemini, ""},
	"openrouter":             {eventschema.ProviderOpenRouter, "openrouter"},
	"fireworks-ai":           {eventschema.ProviderFireworks, "fireworks"},
	"fireworks":              {eventschema.ProviderFireworks, "fireworks"},
	"togetherai":             {eventschema.ProviderTogether, "together"},
	"together":               {eventschema.ProviderTogether, "together"},
	"deepseek":               {eventschema.ProviderDeepSeek, "deepseek"},
	"opencode":               {"opencode", "opencode"},
	"opencode-go":            {"opencode-go", "opencode-go"},
	"zai-coding-plan":        {"zai", "zai"},
	"zai":                    {"zai", "zai-api"},
	"zhipuai-coding-plan":    {"zhipuai", "zhipuai"},
	"zhipuai":                {"zhipuai", "zhipuai-api"},
	"kimi-for-coding":        {"kimi", "kimi"},
	"kimi-code-plan-global":  {"kimi", "kimi"},
	"kimi-code-plan-cn":      {"kimi", "kimi"},
	"moonshotai":             {"moonshot", "moonshot"},
	"moonshotai-cn":          {"moonshot", "moonshot"},
	"minimax-coding-plan":    {"minimax", "minimax"},
	"minimax-cn-coding-plan": {"minimax", "minimax"},
	"minimax":                {"minimax", "minimax-api"},
	"minimax-cn":             {"minimax", "minimax-api"},
	"alibaba-coding-plan":    {"alibaba", "alibaba"},
	"alibaba-coding-plan-cn": {"alibaba", "alibaba"},
	"alibaba":                {"alibaba", "alibaba-api"},
	"alibaba-cn":             {"alibaba", "alibaba-api"},
}

// mapProvider normalizes opencode's providerID to a TokenOps provider. The
// Provider type is an open string, so unknown providers pass through verbatim
// rather than collapsing to "unknown".
func mapProvider(providerID string) eventschema.Provider {
	if m, ok := opencodeProviders[providerID]; ok {
		return m.provider
	}
	if providerID == "" {
		return eventschema.ProviderUnknown
	}
	return eventschema.Provider(providerID)
}

// endpointFor is the endpoint an opencode providerID names, "" when it
// names none (the vendor's own, as before endpoints were recorded).
func endpointFor(providerID string) string {
	return opencodeProviders[providerID].endpoint
}

// Provider is the TokenOps provider and endpoint an opencode providerID
// maps to; ok is false for an ID the mapping does not know.
func Provider(providerID string) (eventschema.Provider, string, bool) {
	m, ok := opencodeProviders[providerID]
	return m.provider, m.endpoint, ok
}

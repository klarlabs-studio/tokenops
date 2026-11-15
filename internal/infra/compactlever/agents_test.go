package compactlever

import (
	"os"
	"path/filepath"
	"testing"
)

// window sizes the models the tests name, as TokenOps' own context-window
// source does for the models it knows; others are unknown.
func window(model string) (int64, bool) {
	switch model {
	case "claude-opus-4-8":
		return 1_000_000, true
	case "claude-haiku-4-5", "gemini-test":
		return 200_000, true
	}
	return 0, false
}

func agentPaths(t *testing.T) Paths {
	t.Helper()
	p := paths(t)
	d := filepath.Dir(filepath.Dir(p.ClaudeSettings))
	for _, sub := range []string{".gemini", ".pi/agent"} {
		if err := os.MkdirAll(filepath.Join(d, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p.GeminiSettings = filepath.Join(d, ".gemini", "settings.json")
	p.PiSettings = filepath.Join(d, ".pi", "agent", "settings.json")
	return p
}

func agentPlan() Plan {
	return Plan{
		GeminiCompactAt: 120_000, PiCompactAt: 400_000,
		PiModels: []string{"anthropic/claude-opus-4-8", "anthropic/claude-haiku-4-5", "openai-codex/gpt-unknown"},
		Window:   window,
	}
}

// Gemini CLI compacts at a fraction of its model's window; Pi keeps
// reserveTokens free below each model's window. Both are written, and a
// revert gives each file back byte for byte.
func TestGeminiAndPiApplyAndRevert(t *testing.T) {
	p := agentPaths(t)
	const geminiOriginal = "{\n  \"model\": {\"name\": \"gemini-test\"},\n  \"ui\": {\"theme\": \"dark\"}\n}\n"
	const piOriginal = "{\"defaultModel\": \"claude-opus-4-8\"}\n"
	write(t, p.GeminiSettings, geminiOriginal)
	write(t, p.PiSettings, piOriginal)

	rs, err := Apply(p, agentPlan(), now)
	if err != nil {
		t.Fatal(err)
	}
	if g := byClient(rs, ClientGemini); len(g) != 1 || g[0].Status != StatusApplied || g[0].Value != 120_000 {
		t.Fatalf("gemini = %+v", g)
	}
	model := jsonOf(t, p.GeminiSettings)["model"].(map[string]any)
	if model["compressionThreshold"] != 0.6 || model["name"] != "gemini-test" {
		t.Errorf("gemini model settings = %v; want compressionThreshold 0.6 (120k of 200k)", model)
	}
	statuses := map[string]Status{}
	for _, r := range byClient(rs, ClientPi) {
		statuses[r.Key] = r.Status
	}
	if statuses["anthropic/claude-opus-4-8"] != StatusApplied || statuses["anthropic/claude-haiku-4-5"] != StatusUnavailable ||
		statuses["openai-codex/gpt-unknown"] != StatusUnavailable {
		t.Errorf("pi statuses = %v; want opus set, haiku already compacting before the line, the unknown model unsized", statuses)
	}
	ov := jsonOf(t, p.PiSettings)["compaction"].(map[string]any)["modelOverrides"].(map[string]any)
	if got := ov["anthropic/claude-opus-4-8"].(map[string]any)["reserveTokens"]; got != float64(600_000) {
		t.Errorf("pi opus reserveTokens = %v; want 1M - 400k", got)
	}
	for _, r := range Check(p) {
		if r.Status != StatusActive {
			t.Errorf("check %s %s = %s", r.Client, r.Key, r.Status)
		}
	}
	if _, err := Revert(p); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{p.GeminiSettings: geminiOriginal, p.PiSettings: piOriginal} {
		if got := read(t, path); got != want {
			t.Errorf("%s after revert:\n%q\nwant\n%q", path, got, want)
		}
	}
}

// A value the operator set, under the current key or an older one, is
// left alone; so is Pi with auto-compaction switched off or a global
// reserve of its own.
func TestGeminiAndPiLeaveTheOperatorsValues(t *testing.T) {
	tests := []struct {
		name, client, file, body string
	}{
		{"gemini current key", ClientGemini, "gemini", `{"model": {"name": "gemini-test", "compressionThreshold": 0.3}}`},
		{"gemini older nested key", ClientGemini, "gemini", `{"model": {"name": "gemini-test", "chatCompression": {"contextPercentageThreshold": 0.4}}}`},
		{"gemini oldest top-level key", ClientGemini, "gemini", `{"model": {"name": "gemini-test"}, "chatCompression": {"contextPercentageThreshold": 0.4}}`},
		{"pi own override", ClientPi, "pi", `{"compaction": {"modelOverrides": {"anthropic/claude-opus-4-8": {"reserveTokens": 50000}}}}`},
		{"pi global reserve", ClientPi, "pi", `{"compaction": {"reserveTokens": 50000}}`},
		{"pi auto-compaction off", ClientPi, "pi", `{"compaction": {"enabled": false}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := agentPaths(t)
			path := p.PiSettings
			if tt.file == "gemini" {
				path = p.GeminiSettings
			}
			write(t, path, tt.body)
			pl := agentPlan()
			pl.PiModels = []string{"anthropic/claude-opus-4-8"}
			rs, err := Apply(p, pl, now)
			if err != nil {
				t.Fatal(err)
			}
			if c := byClient(rs, tt.client); len(c) != 1 || c[0].Status != StatusYours {
				t.Errorf("%s = %+v; want yours", tt.client, c)
			}
			if got := read(t, path); got != tt.body {
				t.Errorf("the operator's file changed:\n%s", got)
			}
		})
	}
}

// Without a measured line neither is written, and a model TokenOps cannot
// size is reported, not guessed.
func TestGeminiAndPiNeedALineAndAKnownWindow(t *testing.T) {
	p := agentPaths(t)
	write(t, p.GeminiSettings, `{"model": {"name": "gemini-unknown"}}`)
	rs, err := Apply(p, Plan{PiModels: []string{"anthropic/claude-opus-4-8"}, Window: window}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(byClient(rs, ClientGemini)) != 0 || len(byClient(rs, ClientPi)) != 0 {
		t.Errorf("wrote without a measured line: %+v", rs)
	}
	rs, err = Apply(p, agentPlan(), now)
	if err != nil {
		t.Fatal(err)
	}
	if g := byClient(rs, ClientGemini); len(g) != 1 || g[0].Status != StatusUnavailable || g[0].Note == "" {
		t.Errorf("gemini with an unsized model = %+v; want unavailable, saying why", g)
	}
}

// An agent not set up here is reported and its directory is not created.
func TestGeminiAndPiNotInstalled(t *testing.T) {
	p := paths(t)
	d := t.TempDir()
	p.GeminiSettings = filepath.Join(d, "nope", ".gemini", "settings.json")
	p.PiSettings = filepath.Join(d, "nope", ".pi", "agent", "settings.json")
	rs, err := Apply(p, agentPlan(), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{ClientGemini, ClientPi} {
		if r := byClient(rs, c); len(r) != 1 || r[0].Status != StatusUnavailable {
			t.Errorf("%s = %+v; want unavailable", c, r)
		}
	}
	if _, err := os.Stat(filepath.Join(d, "nope")); !os.IsNotExist(err) {
		t.Error("created a directory for an agent that is not installed")
	}
}

package compactlever

import (
	"os"
	"testing"
	"time"
)

// Preview says what Apply would set and writes nothing: no agent file and
// no record of a setting. It is the shadow of the ask rung.
func TestPreviewWritesNothing(t *testing.T) {
	p := paths(t)
	const claudeOriginal = "{\"model\": \"opus\"}\n"
	write(t, p.ClaudeSettings, claudeOriginal)
	write(t, p.CodexConfig, codexToml)
	write(t, p.OpencodeConfig, opencodeConfig)
	write(t, p.OpencodeModels, opencodeCatalog)

	rs, err := Preview(p, plan(), now)
	if err != nil {
		t.Fatal(err)
	}
	if c := byClient(rs, ClientClaudeCode); len(c) != 1 || c[0].Status != StatusShadow || c[0].Value != 633_000 {
		t.Errorf("claude preview = %+v; want 633000 as a shadow", c)
	}
	if c := byClient(rs, ClientCodex); len(c) != 1 || c[0].Status != StatusShadow || c[0].Value != 150_000 {
		t.Errorf("codex preview = %+v; want 150000 as a shadow", c)
	}
	for path, want := range map[string]string{p.ClaudeSettings: claudeOriginal, p.CodexConfig: codexToml, p.OpencodeConfig: opencodeConfig} {
		if got := read(t, path); got != want {
			t.Errorf("preview changed %s:\n%q", path, got)
		}
	}
	if _, err := os.Stat(p.State); !os.IsNotExist(err) {
		t.Errorf("preview wrote the state file: %v", err)
	}
	if len(Check(p)) != 0 {
		t.Error("preview recorded a setting")
	}
}

// After Apply, the same plan previews as already in place: nothing more
// would be set.
func TestPreviewOfAnAppliedPlanIsActive(t *testing.T) {
	p := paths(t)
	write(t, p.CodexConfig, codexToml)
	pl := Plan{CodexCompactAt: 150_000}
	if _, err := Apply(p, pl, now); err != nil {
		t.Fatal(err)
	}
	rs, err := Preview(p, pl, now)
	if err != nil {
		t.Fatal(err)
	}
	if c := byClient(rs, ClientCodex); len(c) != 1 || c[0].Status != StatusActive {
		t.Errorf("codex preview after apply = %+v; want active", c)
	}
}

// SetSince is when tokenops first set a compaction line that is still in
// place: the day the setting is judged from.
func TestSetSinceIsTheEarliestSetting(t *testing.T) {
	p := paths(t)
	if _, ok := SetSince(p); ok {
		t.Fatal("a setting before anything was set")
	}
	write(t, p.CodexConfig, codexToml)
	if _, err := Apply(p, Plan{CodexCompactAt: 150_000}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(p, Plan{CodexCompactAt: 150_000, ClaudeCompactAt: 600_000}, now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if at, ok := SetSince(p); !ok || !at.Equal(now) {
		t.Errorf("set since %s, %v; want the first setting at %s", at, ok, now)
	}
}

package hookconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// settingsDoc decodes a settings document the way every reader gets it.
func settingsDoc(t *testing.T, body string) map[string]any {
	t.Helper()
	m, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse(%s): %v", body, err)
	}
	return m
}

// The shapes a hook entry takes on disk. Claude Code nests entries in groups
// with a matcher and carries the verb in args; Codex and Cursor write the whole
// invocation as one command string, sometimes with no type key.
func TestHasAcrossEntryShapes(t *testing.T) {
	cases := []struct {
		name  string
		doc   string
		verb  string
		where Where
		want  bool
	}{
		{
			name:  "claude code nested args on PreToolUse",
			doc:   `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"/usr/local/bin/tokenops","args":["read-guard"]}]}]}}`,
			verb:  "read-guard",
			where: Where{Event: "PreToolUse"},
			want:  true,
		},
		{
			name:  "claude code route guard needs the Agent matcher",
			doc:   `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/x/tokenops","args":["route-guard"]}]}]}}`,
			verb:  "route-guard",
			where: Where{Event: "PreToolUse", Matcher: "Agent"},
			want:  false,
		},
		{
			name:  "route guard on the Agent matcher",
			doc:   `{"hooks":{"PreToolUse":[{"matcher":"Agent","hooks":[{"type":"command","command":"/x/tokenops","args":["route-guard"]}]}]}}`,
			verb:  "route-guard",
			where: Where{Event: "PreToolUse", Matcher: "Agent"},
			want:  true,
		},
		{
			name:  "coach hook is wired on Stop, not elsewhere",
			doc:   `{"hooks":{"PreToolUse":[{"matcher":"","hooks":[{"command":"/x/tokenops","args":["coach-hook"]}]}]}}`,
			verb:  "coach-hook",
			where: Where{Event: "Stop"},
			want:  false,
		},
		{
			name:  "one command string, quoted binary path with spaces",
			doc:   `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"'/Applications/My Tools/tokenops' boundary-guard"}]}]}}`,
			verb:  "boundary-guard",
			where: Where{},
			want:  true,
		},
		{
			name:  "command string with no type key (Cursor)",
			doc:   `{"hooks":{"beforeSubmitPrompt":[{"command":"/x/tokenops boundary-guard"}]}}`,
			verb:  "boundary-guard",
			where: Where{},
			want:  true,
		},
		{
			name:  "a non-command type is not a TokenOps hook",
			doc:   `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"http","command":"/x/tokenops","args":["boundary-guard"]}]}]}}`,
			verb:  "boundary-guard",
			where: Where{},
			want:  false,
		},
		{
			name:  "a foreign hook whose args do not lead with the verb is not ours",
			doc:   `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"command":"/x/other","args":["--note","route-guard"]}]}]}}`,
			verb:  "route-guard",
			where: Where{},
			want:  false,
		},
		{
			name:  "the verb in a string elsewhere in the file does not count",
			doc:   `{"env":{"NOTE":"boundary-guard"},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"command":"/x/other"}]}]}}`,
			verb:  "boundary-guard",
			where: Where{},
			want:  false,
		},
		{
			name:  "no hooks key",
			doc:   `{"model":"opus"}`,
			verb:  "boundary-guard",
			where: Where{},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Has(Hooks(settingsDoc(t, tc.doc)), tc.verb, tc.where); got != tc.want {
				t.Errorf("Has(%s, %+v) = %v, want %v", tc.verb, tc.where, got, tc.want)
			}
		})
	}
}

func TestFindReportsEventMatcherAndCommand(t *testing.T) {
	doc := settingsDoc(t, `{"hooks":{
		"PreToolUse":[{"matcher":"Read","hooks":[{"command":"/x/tokenops","args":["read-guard"]}]},
		                {"matcher":"Read","hooks":[{"command":"/x/tokenops","args":["read-guard"]}]}],
		"Stop":[{"hooks":[{"command":"/x/tokenops coach-hook"}]}]}}`)
	got := Find(Hooks(doc), "read-guard")
	if len(got) != 2 {
		t.Fatalf("Find(read-guard) = %d entries, want 2: %+v", len(got), got)
	}
	for _, loc := range got {
		if loc.Event != "PreToolUse" || loc.Matcher != "Read" || loc.Command != "/x/tokenops" {
			t.Errorf("location %+v, want PreToolUse/Read//x/tokenops", loc)
		}
	}
	if n := len(Find(Hooks(doc), "coach-hook")); n != 1 {
		t.Errorf("Find(coach-hook) = %d, want 1", n)
	}
}

func TestParseTreatsEmptyAsNoSettingsAndRejectsMalformed(t *testing.T) {
	if m, err := Parse([]byte("  \n")); err != nil || len(m) != 0 {
		t.Errorf("Parse(empty) = %v, %v; want empty map and no error", m, err)
	}
	if _, err := Parse([]byte(`{"hooks":`)); err == nil {
		t.Error("Parse(malformed) returned no error")
	}
}

func TestLoadReadsAFileAndFailsOnMissingOrMalformed(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(good, []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		wantErr    bool
	}{
		{"present", good, false},
		{"missing", filepath.Join(dir, "none.json"), true},
		{"malformed", bad, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.path)
			if (err != nil) != tc.wantErr {
				t.Errorf("Load(%s) error = %v, want error %v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestCommandVerbSplitsQuotedPaths(t *testing.T) {
	cases := map[string]string{
		"/usr/local/bin/tokenops route-guard":           "route-guard",
		"'/Applications/My Tools/tokenops' route-guard": "route-guard",
		"tokenops": "",
		"'/a b/tokenops' 'boundary-guard' --client codex": "boundary-guard",
	}
	for cmd, want := range cases {
		if got := CommandVerb(cmd); got != want {
			t.Errorf("CommandVerb(%q) = %q, want %q", cmd, got, want)
		}
	}
}

// Parse and the Find walk must see the same entries whatever the source of
// the JSON, so a settings file decoded here matches the one the CLI decodes.
func TestParseRoundTripsThroughJSON(t *testing.T) {
	src := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"command": "/x/tokenops coach-hook"}}}}}}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	if !Has(Hooks(settingsDoc(t, string(b))), "coach-hook", Where{Event: "Stop"}) {
		t.Errorf("coach-hook not found after round trip: %s", b)
	}
}

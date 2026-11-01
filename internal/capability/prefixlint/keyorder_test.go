package prefixlint

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalJSON(t *testing.T) {
	cases := []struct {
		name     string
		a, b     string
		same     bool
		withJSON bool
	}{
		{"keys reordered", `{"b":1,"a":{"y":2,"x":3}}`, `{"a":{"x":3,"y":2},"b":1}`, true, true},
		{"spacing", `{"a": 1}`, `{"a":1}`, true, true},
		{"a JSON line in text", "rules:\n{\"b\":1,\"a\":2}\nend", "rules:\n{\"a\":2,\"b\":1}\nend", true, true},
		{"a value changed", `{"a":1}`, `{"a":2}`, false, true},
		{"array order is content", `[1,2]`, `[2,1]`, false, true},
		{"plain text", "3 open items", "4 open items", false, false},
		{"large numbers keep their digits", `{"n":12345678901234567890}`, `{"n":12345678901234567891}`, false, true},
	}
	for _, c := range cases {
		ca, ja := canonicalJSON(c.a)
		cb, jb := canonicalJSON(c.b)
		if (ca == cb) != c.same || ja != c.withJSON || jb != c.withJSON {
			t.Errorf("%s: canonical %q vs %q (json %v %v), want same=%v json=%v", c.name, ca, cb, ja, jb, c.same, c.withJSON)
		}
	}
}

// A SessionStart hook that prints the same JSON with its keys in another
// order each session breaks the prefix as surely as a timestamp, and is
// named for what it is rather than as output that varies.
func TestLintFlagsHookOutputWithUnstableKeyOrder(t *testing.T) {
	fx := newFixture(t)
	root := filepath.Join(fx.home, ".claude", "projects")
	fx.src.TranscriptRoot = root
	settings := filepath.Join(fx.src.ClaudeDir, "settings.json")
	settingsWithHooks(t, settings, map[string][]string{EventSessionStart: {"~/bin/state.pl", "~/bin/varies.sh"}})
	ctx := func(s string) string {
		b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "SessionStart", "additionalContext": s}})
		return string(b)
	}
	transcript(t, root, "s1",
		hookRecord("SessionStart:startup", "~/bin/state.pl", ctx(`{"project":"private-name","branch":"main","open":3}`)),
		hookRecord("SessionStart:startup", "~/bin/varies.sh", "rules\n{\"open\":3}"),
	)
	transcript(t, root, "s2",
		hookRecord("SessionStart:startup", "~/bin/state.pl", ctx(`{"open":3,"branch":"main","project":"private-name"}`)),
		hookRecord("SessionStart:startup", "~/bin/varies.sh", "rules\n{\"open\":4}"),
	)
	transcript(t, root, "s3",
		hookRecord("SessionStart:startup", "~/bin/state.pl", ctx(`{"branch":"main","project":"private-name","open":3}`)),
	)

	r := Lint(fx.src)
	var got []Finding
	for _, f := range r.Findings {
		if f.Source == SourceHookOutput {
			got = append(got, f)
		}
	}
	if len(got) != 2 {
		t.Fatalf("hook-output findings = %+v, want the unstable JSON and the varying output", got)
	}
	f := got[0]
	if got[1].Kind != KindOutputVaries || got[1].Command != "varies.sh" {
		t.Errorf("a changed value is output that varies, not key order: %+v", got[1])
	}
	if f.Kind != KindKeyOrder || f.Severity != SeverityHigh || f.Command != "state.pl" || f.Path != settings ||
		f.Token != "3 key orders in 3 sessions" || f.Fix == "" || f.Why == "" {
		t.Errorf("finding = %+v", f)
	}
	if got := f.Subject(); got != "SessionStart hook `state.pl` printing JSON whose key order changes" {
		t.Errorf("subject = %q", got)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "private-name") {
		t.Error("report carries hook output")
	}
}

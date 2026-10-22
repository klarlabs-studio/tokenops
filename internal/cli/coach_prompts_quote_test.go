package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coachSecretKey is an Anthropic-shaped key an operator pasted into a
// prompt. It is fake, but matches the redactor's pattern.
const coachSecretKey = "sk-ant-api03-ZZZZYYYYXXXXWWWWVVVVUUUUTTTTSSSSRRRRQQQQ"

// writeSecretPromptFixture writes one Claude Code transcript whose
// operator turns carry coachSecretKey: repeated (a repeated-prompt
// sample), as a rejection (a regenerate sample) and short (a vague
// sample).
func writeSecretPromptFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "projects")
	turn := func(ts, text string) string {
		return `{"type":"user","timestamp":"` + ts + `","sessionId":"s9","cwd":"/work/app","message":{"content":"` + text + `"}}`
	}
	lines := []string{
		turn("2026-01-05T10:00:00Z", "deploy with key "+coachSecretKey),
		turn("2026-01-05T10:01:00Z", "deploy with key "+coachSecretKey),
		turn("2026-01-05T10:02:00Z", "deploy with key "+coachSecretKey),
		turn("2026-01-05T10:03:00Z", "wrong, the key is "+coachSecretKey),
		turn("2026-01-05T10:04:00Z", "fix it"),
	}
	full := filepath.Join(root, "proj-s", "s9.jsonl")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// By default `coach prompts` prints counts and recommendations but no
// prompt text, in either format; --quote adds the samples, redacted.
func TestCoachPromptsQuotesOnlyOnRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := writeSecretPromptFixture(t)
	base := []string{"coach", "prompts", "--root", root, "--source", "claude-code", "--since", "2026-01-01T00:00:00Z"}

	for _, format := range [][]string{nil, {"--json"}} {
		out, err := executeRoot(t, append(append([]string{}, base...), format...)...)
		if err != nil {
			t.Fatalf("%v: %v", format, err)
		}
		if strings.Contains(out, "sk-ant-") {
			t.Errorf("%v: output carries the key:\n%s", format, out)
		}
		if strings.Contains(out, "deploy with key") || strings.Contains(out, "the key is") {
			t.Errorf("%v: output quotes prompt text without --quote:\n%s", format, out)
		}
		if !strings.Contains(out, "3") {
			t.Errorf("%v: counts missing:\n%s", format, out)
		}
	}

	for _, format := range [][]string{nil, {"--json"}} {
		args := append(append(append([]string{}, base...), "--quote"), format...)
		out, err := executeRoot(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(out, "sk-ant-") {
			t.Errorf("%v: quoted output carries the key:\n%s", format, out)
		}
		// encoding/json escapes the placeholder's angle brackets.
		out = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">").Replace(out)
		if !strings.Contains(out, "deploy with key <redacted:anthropic_api_key>") {
			t.Errorf("%v: --quote did not quote the redacted prompt:\n%s", format, out)
		}
	}
}

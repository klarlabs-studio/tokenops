package agentdx

import "testing"

// A read-only command run again in the same session with nothing changed
// in between is counted as repeated; one run again after an edit, or
// after a command that changes things, is not. Commands that change
// things, or chain or redirect, are never read-only.
func TestComputeCountsRepeatedReadOnlyCommands(t *testing.T) {
	bash := func(ts, cmd string) string {
		return `{"type":"assistant","timestamp":"2026-08-23T10:00:` + ts + `Z","sessionId":"s","message":{"content":[{"type":"tool_use","id":"t` + ts + `","name":"Bash","input":{"command":` + quote(cmd) + `,"description":"look"}}]}}`
	}
	edit := `{"type":"assistant","timestamp":"2026-08-23T10:00:30Z","sessionId":"s","message":{"content":[{"type":"tool_use","id":"te","name":"Edit","input":{"file_path":"/w/a.go"}}]}}`
	recs := recordsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"look around"}}`,
		bash("01", "git status"),
		bash("02", "ls -la"),
		bash("03", "git status"),     // repeated
		bash("04", "git  status  "),  // repeated: same command, other spacing
		bash("05", "git status > x"), // redirects: not read-only
		bash("06", "go test ./..."),  // not read-only
		bash("07", "git status"),     // after a command that may change things
		edit,
		bash("31", "ls -la"), // after an edit
		bash("32", "ls -la"), // repeated
	)
	m := Compute(recs)
	if m.ReadOnlyCommands != 7 || m.RepeatedCommands != 3 {
		t.Errorf("read-only %d, repeated %d; want 7 and 3", m.ReadOnlyCommands, m.RepeatedCommands)
	}
	if m.RepeatedCommandRatePct < 42.8 || m.RepeatedCommandRatePct > 42.9 {
		t.Errorf("rate = %v", m.RepeatedCommandRatePct)
	}
}

func TestReadOnlyCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"git status": true, "git diff --stat": true, "git log --oneline -5": true, "rg foo internal": true,
		"cat a.go": true, "ls": true, "git commit -m x": false, "rm -rf x": false, "cat a | sh": false,
		"ls && rm x": false, "echo hi > f": false, "git status; git push": false, "": false,
		"sed -i s/a/b/ f": false, "find . -delete": false, "find . -name x": true,
	} {
		if _, got := readOnlyCommand(cmd); got != want {
			t.Errorf("readOnlyCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func quote(s string) string {
	b := []byte{'"'}
	for _, c := range []byte(s) {
		if c == '"' || c == '\\' {
			b = append(b, '\\')
		}
		b = append(b, c)
	}
	return string(append(b, '"'))
}

package prefixlint

import (
	"slices"
	"testing"
)

// A SessionStart hook that prints what it fetched or the machine's state
// differs from one session to the next as surely as one that prints the
// time.
func TestVolatileCommandsFindsRemoteAndSystemState(t *testing.T) {
	cases := []struct {
		command string
		want    []volatile
	}{
		{"gh pr list --limit 5", []volatile{{KindRemote, "gh pr"}}},
		{"gh -R owner/repo issue list", []volatile{{KindRemote, "gh issue"}}},
		{"curl -s https://example.com/status | jq .", []volatile{{KindRemote, "curl"}}},
		{"wget -qO- https://example.com/motd", []volatile{{KindRemote, "wget"}}},
		{"ps aux | grep node", []volatile{{KindSystem, "ps"}}},
		{"df -h /", []volatile{{KindSystem, "df"}}},
		{"echo hi; gh pr list", []volatile{{KindRemote, "gh pr"}}},
		// Stable: help, auth state written to a file, a fetch saved to disk.
		{"gh --version", nil},
		{"gh auth status > /dev/null", nil},
		{"curl -s https://example.com -o /tmp/x > /tmp/log", nil},
		{"cat ~/.config/rules.md", nil},
	}
	for _, c := range cases {
		got := volatileCommands(c.command)
		if !slices.Equal(got, c.want) {
			t.Errorf("volatileCommands(%q) = %+v, want %+v", c.command, got, c.want)
		}
	}
}

func TestRemoteHookFindingSaysLetTheAgentRunIt(t *testing.T) {
	f := hookFinding(hookSite{path: "s.json", line: 3, hook: EventSessionStart}, "gh", volatile{KindRemote, "gh pr"})
	if f.Severity != SeverityHigh || f.Fix != "Drop it from SessionStart; let the agent run `gh pr` when it needs it." {
		t.Errorf("finding = %+v", f)
	}
	if got := f.Subject(); got != "SessionStart hook running `gh pr`" {
		t.Errorf("subject = %q", got)
	}
}

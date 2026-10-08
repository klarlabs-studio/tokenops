package teamshare

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

var now = time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)

type fakeRepos map[string][2]string // dir -> {root, origin}

func (f fakeRepos) Root(_ context.Context, dir string) string { return f[dir][0] }
func (f fakeRepos) OriginName(_ context.Context, root string) string {
	for _, v := range f {
		if v[0] == root {
			return v[1]
		}
	}
	return ""
}

// Canaries: every one of these is on the machine and must not be in the
// upload.
var canaries = []string{
	"fix the login bug", "hunter2", "/Users/alice", "secret-project", "auth.go",
	"claude-opus", "sess-1", "Read", "github.com",
}

func deps() Deps {
	t0 := now.Add(-2 * time.Hour)
	units := []sessions.Unit{
		{SessionID: "sess-1", Prompt: "fix the login bug, password hunter2", Start: t0, End: t0.Add(4 * time.Minute),
			Turns: 3, ToolCalls: 2, Files: []string{"/Users/alice/secret-project/auth.go"}, Tools: []string{"Read"}},
		{SessionID: "sess-1", Prompt: "continue", Start: t0.Add(10 * time.Minute), End: t0.Add(12 * time.Minute), Turns: 1, Reworked: true},
		{SessionID: "sess-2", Prompt: "refactor the parser", Start: t0, End: t0.Add(time.Minute), Turns: 2},
		// Older than the window: left out.
		{SessionID: "sess-2", Prompt: "list files", Start: now.AddDate(0, 0, -30), Turns: 1},
	}
	turns := []analytics.SessionTurn{
		{SessionID: "sess-1", At: t0.Add(time.Minute), Model: "claude-opus", Tokens: 1000, CostUSD: 0, APIEquivalentUSD: 0.5, Priced: true},
		{SessionID: "sess-1", At: t0.Add(11 * time.Minute), Tokens: 200, Priced: false},
		{SessionID: "sess-3", At: t0, Tokens: 50, Priced: true, APIEquivalentUSD: 0.01},
	}
	return Deps{
		Units: func(_, _ time.Time) ([]sessions.Unit, error) { return units, nil },
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return turns, nil },
		Dirs: func(time.Time) map[string]sessiondirs.Dir {
			return map[string]sessiondirs.Dir{
				"sess-1": {CWD: "/Users/alice/secret-project/sub", Branch: "feat/secret"},
				"sess-2": {CWD: "/tmp/scratch"},
			}
		},
		Repos: fakeRepos{
			"/Users/alice/secret-project/sub": {"/Users/alice/secret-project", "acme/api"},
			"/tmp/scratch":                    {"", ""},
		},
	}
}

// TestUploadCarriesNoContent builds an upload from a machine full of
// prompts, paths, file names, model names and session IDs, and checks none
// of them crossed.
func TestUploadCarriesNoContent(t *testing.T) {
	u, warnings := Build(context.Background(), deps(), Options{Days: 14, RepoNames: RepoRemote, ClientVersion: "0.102.0"}, now)
	if len(warnings) > 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if err := u.Validate(); err != nil {
		t.Fatalf("upload does not validate: %v", err)
	}
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range canaries {
		if strings.Contains(string(raw), c) {
			t.Errorf("upload carries %q:\n%s", c, raw)
		}
	}
}

func TestAggregateFilesByRepoAndKind(t *testing.T) {
	u, _ := Build(context.Background(), deps(), Options{Days: 14, RepoNames: RepoRemote}, now)
	got := map[string]teamwire.Bucket{}
	for _, b := range u.Buckets {
		got[b.Repo+"|"+b.Kind] = b
	}
	edit := got["acme/api|edit"]
	// "continue" keeps the kind of the work in flight: both are edits.
	if edit.Instructions != 2 || edit.Turns != 4 || edit.FirstTry != 1 || edit.Reworked != 1 || edit.Sessions != 1 {
		t.Errorf("edit bucket = %+v", edit)
	}
	if edit.Tokens != 1200 || edit.APIEquivalentUSD != 0.5 || edit.UnpricedTurns != 1 || edit.ActiveSeconds != 360 {
		t.Errorf("edit money = %+v", edit)
	}
	if deep := got["none|deep"]; deep.Instructions != 1 {
		t.Errorf("work outside a repository = %+v", deep)
	}
	if unk := got["none|unknown"]; unk.Tokens != 50 {
		t.Errorf("turns of an unknown session = %+v", unk)
	}
	if len(u.Days) != 14 || u.Days[13] != "2026-10-08" {
		t.Errorf("days = %v", u.Days)
	}
}

func TestRepoLabels(t *testing.T) {
	repos := fakeRepos{
		"/w/a": {"/w/a", "acme/api"},
		"/w/b": {"/w/my repo!", ""},
		"/w/c": {"/w/c", "../../etc/passwd"},
		"/w/d": {"", ""},
	}
	ctx := context.Background()
	for _, c := range []struct{ dir, mode, want string }{
		{"/w/a", RepoRemote, "acme/api"},
		{"/w/a", RepoDirectory, "a"},
		{"/w/a", RepoHidden, teamwire.RepoHidden},
		{"/w/b", RepoRemote, "my-repo"},
		// A remote that is not owner/name falls back to the directory.
		{"/w/c", RepoRemote, "c"},
		{"/w/d", RepoRemote, teamwire.RepoNone},
		{"", RepoRemote, teamwire.RepoNone},
	} {
		if got := repoLabel(ctx, repos, c.dir, c.mode); got != c.want || !teamwire.ValidRepo(got) {
			t.Errorf("repoLabel(%s, %s) = %q, want %q", c.dir, c.mode, got, c.want)
		}
	}
}

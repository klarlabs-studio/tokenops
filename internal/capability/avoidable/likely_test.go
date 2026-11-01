package avoidable

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/prefixlint"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

// The prefix-change rebuild in causesFixture is session "hour"'s; it ran
// in Claude Code in /w/app, a repository.
func TestPrefixChangesNameTheLikelyLintFinding(t *testing.T) {
	sessionStartDate := prefixlint.Finding{Severity: prefixlint.SeverityHigh, Source: prefixlint.SourceHook,
		Clients: []string{prefixlint.ClientClaudeCode}, Hook: prefixlint.EventSessionStart, Kind: prefixlint.KindClock, Token: "date"}
	promptDate := sessionStartDate
	promptDate.Hook, promptDate.Severity = prefixlint.EventUserPromptSubmit, prefixlint.SeverityMedium
	codexFile := prefixlint.Finding{Severity: prefixlint.SeverityLow, Source: prefixlint.SourceFile,
		Clients: []string{prefixlint.ClientCodex}, Kind: prefixlint.KindDate, Token: "2026-10-01"}
	projectFile := prefixlint.Finding{Severity: prefixlint.SeverityMedium, Source: prefixlint.SourceFile,
		Clients: []string{prefixlint.ClientClaudeCode}, Kind: prefixlint.KindLastUpdated, Token: "Last updated"}

	tests := []struct {
		name string
		// lint is what the lint finds, by root ("" the user's files).
		lint map[string][]prefixlint.Finding
		dirs bool
		want []string
	}{
		{name: "a SessionStart hook in the user's settings", dirs: true,
			lint: map[string][]prefixlint.Finding{"/w/app": {sessionStartDate}},
			want: []string{"SessionStart hook running `date`"}},
		{name: "a line in the project's context file", dirs: true,
			lint: map[string][]prefixlint.Finding{"/w/app": {projectFile}, "": {}},
			want: []string{"last-updated in a context file"}},
		{name: "another project's finding is not joined", dirs: true,
			lint: map[string][]prefixlint.Finding{"/w/other": {projectFile}}},
		{name: "no finding", dirs: true, lint: map[string][]prefixlint.Finding{}},
		{name: "a per-turn hook is appended, not in the prefix", dirs: true,
			lint: map[string][]prefixlint.Finding{"/w/app": {promptDate}}},
		{name: "a file only another harness loads", dirs: true,
			lint: map[string][]prefixlint.Finding{"/w/app": {codexFile}}},
		{name: "directory unknown: the user's files only",
			lint: map[string][]prefixlint.Finding{"/w/app": {projectFile}, "": {sessionStartDate}},
			want: []string{"SessionStart hook running `date`"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := causesFixture()
			for i := range in.Turns {
				in.Turns[i].Source = "claude-code-jsonl"
			}
			if tt.dirs {
				in.Dirs = map[string]sessiondirs.Dir{"hour": {CWD: "/w/app/web"}}
				in.RootOf = map[string]string{"/w/app/web": "/w/app"}
			}
			linted := map[string]int{}
			in.Lint = func(root string) []prefixlint.Finding {
				linted[root]++
				return tt.lint[root]
			}
			prefix, ok := causeOf(class(Build(in), waste.ClassCacheRebuild), waste.CausePrefixChanged)
			if !ok {
				t.Fatal("no prefix-change rebuild in the fixture")
			}
			if len(prefix.Likely) != len(tt.want) {
				t.Fatalf("likely = %+v, want %v", prefix.Likely, tt.want)
			}
			for i, w := range tt.want {
				if got := prefix.Likely[i]; got.Finding != w || got.Rebuilds != 1 || got.Sessions != 1 {
					t.Errorf("likely[%d] = %+v, want %q on 1 rebuild in 1 session", i, got, w)
				}
			}
			if (prefix.LikelyMethod != "") != (len(tt.want) > 0) {
				t.Errorf("method = %q", prefix.LikelyMethod)
			}
			for root, n := range linted {
				if n != 1 {
					t.Errorf("linted %q %d times, want once", root, n)
				}
			}
			// Only the project with a prefix change is linted.
			if len(linted) != 1 {
				t.Errorf("linted %v, want one root", linted)
			}
		})
	}
}

// Without a linter, nothing is named.
func TestPrefixChangesWithoutALinter(t *testing.T) {
	prefix, _ := causeOf(class(Build(causesFixture()), waste.ClassCacheRebuild), waste.CausePrefixChanged)
	if prefix.Likely != nil || prefix.LikelyMethod != "" {
		t.Errorf("likely = %+v", prefix)
	}
}

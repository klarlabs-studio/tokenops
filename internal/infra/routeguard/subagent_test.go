package routeguard

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func subagent(prompt, requested, session string) SubagentInput {
	return SubagentInput{
		Prompt: prompt, Requested: requested, SessionModel: session,
		Provider: eventschema.ProviderAnthropic, Catalog: catalog(),
		Candidates: []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5", "claude-fable-5-1"},
	}
}

// A lookup subagent inheriting a flagship session moves to the cheap tier,
// expressed as an alias the Agent tool accepts.
func TestSubagentLookupMovesDown(t *testing.T) {
	d := EvaluateSubagent(subagent("find where the retention config is defined", "", "claude-opus-5"))
	if !d.Rewrite || d.To != "claude-haiku-4-5" || d.ToAlias != "haiku" || d.From != "claude-opus-5" {
		t.Fatalf("decision = %+v", d)
	}
}

// The agent writes a subagent's prompt as a long instruction, which the
// turn classifier abstains on; the Agent tool's short description reads
// like a person's request. Found in a real session: "Find retention setting
// file" is lookup work, the multi-sentence prompt beside it classified as
// nothing, and the subagent stayed on the session's model.
func TestSubagentClassifiesTheDescriptionFirst(t *testing.T) {
	in := subagent(`In the current working directory (a git repo), find where the "retention" setting is defined — likely a config file such as retention.yaml or similar. Search the directory for it, confirm which file contains the setting, and reply with only the file name.`, "", "claude-opus-5")
	in.Description = "Find retention setting file"
	if d := EvaluateSubagent(in); !d.Rewrite || d.ToAlias != "haiku" {
		t.Fatalf("decision = %+v", d)
	}
}

// A requested alias is resolved against the offered models.
func TestSubagentRequestedAliasIsTheStartingPoint(t *testing.T) {
	d := EvaluateSubagent(subagent("research how prefix keys work and summarise", "opus", "claude-haiku-4-5"))
	if !d.Rewrite || d.ToAlias != "sonnet" || d.From != "claude-opus-5" {
		t.Fatalf("decision = %+v", d)
	}
}

// Never up: work that needs more than the subagent's model is left alone,
// and so is work that fits.
func TestSubagentNeverMovesUp(t *testing.T) {
	for _, in := range []SubagentInput{
		subagent("refactor the router across every provider", "haiku", "claude-opus-5"),
		subagent("refactor the router across every provider", "", "claude-opus-5"),
		subagent("find the config file", "haiku", "claude-opus-5"),
	} {
		if d := EvaluateSubagent(in); d.Rewrite {
			t.Errorf("%q from %q rewritten to %q", in.Prompt, in.Requested, d.To)
		}
	}
}

// A model the tool cannot name by alias, an unknown kind, or an unplaceable
// model means no rewrite: guessing is worse than leaving it.
func TestSubagentAbstains(t *testing.T) {
	for name, in := range map[string]SubagentInput{
		"unplaceable":   subagent("find the config file", "", "some-other-model"),
		"no candidates": {Prompt: "find the config file", SessionModel: "claude-opus-5", Provider: eventschema.ProviderAnthropic, Catalog: catalog()},
		"no catalog":    {Prompt: "find the config file", SessionModel: "claude-opus-5", Provider: eventschema.ProviderAnthropic, Candidates: []string{"claude-haiku-4-5"}},
	} {
		if d := EvaluateSubagent(in); d.Rewrite {
			t.Errorf("%s: rewrote to %q", name, d.To)
		}
	}
}

// Every family the Agent tool can name forms a move kind: the retraction
// check is asked about the move by family on both sides, so a move off
// Fable is judged like a move off Opus. Found in a real ledger: research
// moves fable → sonnet carried an empty From and nothing judged them.
func TestSubagentMoveKindNamesBothFamilies(t *testing.T) {
	tests := []struct {
		name, prompt, session string
		wantKind, wantFrom    string
		wantTo                string
	}{
		{"opus research", "research how prefix keys work and summarise", "claude-opus-5", "research", "opus", "sonnet"},
		{"fable research", "research how prefix keys work and summarise", "claude-fable-5-2", "research", "fable", "sonnet"},
		{"fable lookup", "find where the retention config is defined", "claude-fable-5-2", "lookup", "fable", "haiku"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := subagent(tt.prompt, "", tt.session)
			in.Candidates = []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5", tt.session}
			var asked []string
			in.Retracted = func(kind, from, to string) bool {
				asked = append(asked, kind+": "+from+" → "+to)
				return false
			}
			d := EvaluateSubagent(in)
			if !d.Rewrite || d.ToAlias != tt.wantTo {
				t.Fatalf("decision = %+v", d)
			}
			want := tt.wantKind + ": " + tt.wantFrom + " → " + tt.wantTo
			if len(asked) == 0 || asked[0] != want {
				t.Fatalf("retraction asked %q, want %q", asked, want)
			}
			if got := AliasFor(d.From); got != tt.wantFrom {
				t.Fatalf("AliasFor(%q) = %q, want %q", d.From, got, tt.wantFrom)
			}
		})
	}
}

// A requested "fable" resolves to the offered Fable model.
func TestSubagentRequestedFableResolves(t *testing.T) {
	d := EvaluateSubagent(subagent("research how prefix keys work and summarise", "fable", "claude-haiku-4-5"))
	if !d.Rewrite || d.From != "claude-fable-5-1" || d.ToAlias != "sonnet" {
		t.Fatalf("decision = %+v", d)
	}
}

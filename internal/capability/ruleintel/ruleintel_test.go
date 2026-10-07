package ruleintel

import (
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func writeCorpus(t *testing.T) Corpus {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"CLAUDE.md": "# Testing\nuse tdd\n## Style\nbe concise\n",
		"AGENTS.md": "# Testing\nuse tdd\n",
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Corpus{Root: dir, RepoID: "repo"}
}

func TestProvider(t *testing.T) {
	tests := map[string]eventschema.Provider{
		"anthropic": eventschema.ProviderAnthropic,
		"gemini":    eventschema.ProviderGemini,
		"openai":    eventschema.ProviderOpenAI,
		"":          eventschema.ProviderOpenAI,
		"mistral":   eventschema.ProviderOpenAI,
	}
	for in, want := range tests {
		if got := Provider(in); got != want {
			t.Errorf("Provider(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnalyzeFindsTheRepeatedSection(t *testing.T) {
	res, err := Analyze(writeCorpus(t), eventschema.ProviderAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Documents) != 2 {
		t.Fatalf("documents = %d, want 2", len(res.Documents))
	}
	if len(res.DuplicateGroups) == 0 {
		t.Error("the shared '# Testing / use tdd' section should form a duplicate group")
	}
}

func TestCompressReportsEveryDocument(t *testing.T) {
	c := writeCorpus(t)
	res, err := Compress(c, CompressOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(res.Results))
	}
	for _, r := range res.Results {
		if r.Path == "" || r.SourceID == "" {
			t.Errorf("result missing identity: %+v", r)
		}
	}
}

func TestInjectUsesTheCorpusRepository(t *testing.T) {
	res, err := Inject(writeCorpus(t), InjectQuery{Keywords: []string{"testing"}, IncludeGlobalScope: true})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.Considered == 0 {
		t.Fatalf("selection considered nothing: %+v", res)
	}
}

func TestConflictsOnAnEmptyCorpus(t *testing.T) {
	res, err := DetectConflicts(Corpus{Root: t.TempDir(), RepoID: "repo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("findings = %v, want none", res.Findings)
	}
}

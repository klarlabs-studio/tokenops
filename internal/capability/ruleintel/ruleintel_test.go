package ruleintel

import (
	"errors"
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

func TestParseProviderRefusesWhatProviderWouldDefault(t *testing.T) {
	if p, ok := ParseProvider("gemini"); !ok || p != eventschema.ProviderGemini {
		t.Errorf("gemini = %q, %v", p, ok)
	}
	if p, ok := ParseProvider(""); !ok || p != eventschema.ProviderOpenAI {
		t.Errorf("empty = %q, %v", p, ok)
	}
	if _, ok := ParseProvider("mistral"); ok {
		t.Error("mistral should be refused")
	}
}

// CompressDetailed is Compress plus sections, and the body only when it
// is asked for.
func TestCompressDetailedAgreesWithCompress(t *testing.T) {
	c := writeCorpus(t)
	summary, err := Compress(c, CompressOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bare, err := CompressDetailed(c, CompressOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	withBody, err := CompressDetailed(c, CompressOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range bare {
		if d.CompressedDocument != summary.Results[i] {
			t.Errorf("document %d: detailed %+v, summary %+v", i, d.CompressedDocument, summary.Results[i])
		}
		if len(d.Sections) == 0 || d.Body != "" {
			t.Errorf("document %d: sections %d, body %q", i, len(d.Sections), d.Body)
		}
		if withBody[i].Body == "" {
			t.Errorf("document %d: no body when asked for one", i)
		}
	}
}

// A CorpusError reads as the read failure it wraps, so marking one does
// not change the message a caller sees.
func TestCorpusErrorReadsAsItsCause(t *testing.T) {
	cause := os.ErrPermission
	var err error = &CorpusError{Err: cause}
	if err.Error() != cause.Error() || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %q, want %q wrapping it", err, cause)
	}
}

func TestBenchRefusesABadSpec(t *testing.T) {
	if _, err := Bench([]byte("profiles: [")); err == nil {
		t.Fatal("a malformed spec should be refused")
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

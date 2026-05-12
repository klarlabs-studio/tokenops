package redaction

import (
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestRedactDetectsKnownProviderKeys(t *testing.T) {
	r := Default()
	cases := []struct {
		name string
		in   string
		kind Kind
	}{
		{"openai", "key=sk-proj-AbCDefGhIJKLmnopQRSTUVwxyz1234567890", KindOpenAIKey},
		{"anthropic", "header sk-ant-api03-aB1cD2eF3gH4iJ5kL6mN7oP8qR9sT0uV1wX2yZ3aB4cD5eF6gH7", KindAnthropicKey},
		{"gemini", "k=AIzaSyDxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", KindGeminiKey},
		{"github", "token=ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789", KindGitHubToken},
		{"aws-access", "AKIAIOSFODNN7EXAMPLE was leaked", KindAWSAccessKey},
		{"jwt", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", KindJWT},
		{"email", "alice@example.com", KindEmail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, findings := r.Redact(tc.in)
			if len(findings) == 0 {
				t.Fatalf("no findings for %s", tc.in)
			}
			matched := false
			for _, f := range findings {
				if f.Kind == tc.kind {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("kind %s not in findings: %+v", tc.kind, findings)
			}
			if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") ||
				strings.Contains(got, "sk-ant-api03-aB1cD2") ||
				strings.Contains(got, "AIzaSy") {
				t.Errorf("redaction left raw secret in output: %s", got)
			}
			if !strings.Contains(got, "<redacted:") {
				t.Errorf("placeholder missing in output: %s", got)
			}
		})
	}
}

func TestRedactBearerCapturesOnlyToken(t *testing.T) {
	r := Default()
	in := "Authorization: Bearer abcDEF1234567890ghijKLMN"
	got, findings := r.Redact(in)
	if len(findings) == 0 {
		t.Fatal("expected bearer finding")
	}
	if !strings.Contains(got, "Bearer <redacted:bearer_token>") {
		t.Errorf("expected literal Bearer prefix preserved, got: %s", got)
	}
}

func TestRedactAWSSecretCapturesValue(t *testing.T) {
	r := Default()
	in := `aws_secret_access_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`
	got, findings := r.Redact(in)
	if len(findings) == 0 {
		t.Fatal("expected aws secret finding")
	}
	if strings.Contains(got, "wJalrXUtnFEMI") {
		t.Errorf("raw secret leaked: %s", got)
	}
}

func TestRedactPlainTextUntouched(t *testing.T) {
	r := New(Config{EntropyEnabled: boolPtr(false)})
	in := "this is a perfectly ordinary sentence with no secrets in it"
	got, findings := r.Redact(in)
	if got != in {
		t.Errorf("plain text was modified: %s", got)
	}
	if len(findings) != 0 {
		t.Errorf("unexpected findings: %+v", findings)
	}
}

func TestEntropyDetectsHighEntropyToken(t *testing.T) {
	r := Default()
	// 32 random base64 chars — should land above the 4.5 bits/char threshold.
	in := "token: G7v9q3LpZ2yN8wX4cR1bV5tH6sJ0aFeQ"
	_, findings := r.Redact(in)
	found := false
	for _, f := range findings {
		if f.Kind == KindHighEntropy {
			found = true
		}
	}
	if !found {
		t.Errorf("expected high-entropy finding, got: %+v", findings)
	}
}

func TestEntropyCanBeDisabled(t *testing.T) {
	r := New(Config{EntropyEnabled: boolPtr(false)})
	in := "token: G7v9q3LpZ2yN8wX4cR1bV5tH6sJ0aFeQ"
	_, findings := r.Redact(in)
	for _, f := range findings {
		if f.Kind == KindHighEntropy {
			t.Errorf("entropy should be disabled but found: %+v", f)
		}
	}
}

func TestDetectDoesNotMutate(t *testing.T) {
	r := Default()
	in := "key=sk-proj-AbCDefGhIJKLmnopQRSTUVwxyz1234567890"
	findings := r.Detect(in)
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}
	// Detect must not allocate a placeholder string into the input.
	if !strings.Contains(in, "sk-proj-") {
		t.Errorf("Detect mutated input: %s", in)
	}
}

func TestMergeFindingsResolvesOverlaps(t *testing.T) {
	in := []Finding{
		{Kind: KindOpenAIKey, Start: 5, End: 30, Match: "X"},
		{Kind: KindHighEntropy, Start: 10, End: 25, Match: "Y"},
		{Kind: KindEmail, Start: 50, End: 60, Match: "Z"},
	}
	got := mergeFindings(in)
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(got), got)
	}
	if got[0].Kind != KindOpenAIKey || got[1].Kind != KindEmail {
		t.Errorf("unexpected kinds: %+v", got)
	}
}

func TestRedactEnvelopePromptFields(t *testing.T) {
	r := Default()
	env := &eventschema.Envelope{
		ID:            "e-1",
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     time.Now().UTC(),
		Source:        "proxy",
		Attributes: map[string]string{
			"deploy": "alice@example.com",
		},
		Payload: &eventschema.PromptEvent{
			PromptHash:   "sha256:abc",
			Provider:     eventschema.ProviderOpenAI,
			RequestModel: "gpt-4o",
			UserID:       "user@example.com",
			AgentID:      "AKIAIOSFODNN7EXAMPLE",
		},
	}
	findings := r.RedactEnvelope(env)
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}
	pe := env.Payload.(*eventschema.PromptEvent)
	if strings.Contains(pe.UserID, "@") || strings.Contains(pe.UserID, "example.com") {
		t.Errorf("UserID not redacted: %s", pe.UserID)
	}
	if strings.Contains(pe.AgentID, "AKIA") {
		t.Errorf("AgentID not redacted: %s", pe.AgentID)
	}
	if strings.Contains(env.Attributes["deploy"], "@") {
		t.Errorf("attribute not redacted: %s", env.Attributes["deploy"])
	}
}

func TestRedactEnvelopeNilSafe(t *testing.T) {
	r := Default()
	if got := r.RedactEnvelope(nil); got != nil {
		t.Errorf("nil envelope: got %+v, want nil", got)
	}
}

func TestRedactEnvelopeAllPayloadTypes(t *testing.T) {
	r := Default()
	cases := []*eventschema.Envelope{
		{
			ID: "p", Type: eventschema.EventTypePrompt, Timestamp: time.Now().UTC(),
			Payload: &eventschema.PromptEvent{UserID: "leak@example.com"},
		},
		{
			ID: "w", Type: eventschema.EventTypeWorkflow, Timestamp: time.Now().UTC(),
			Payload: &eventschema.WorkflowEvent{
				WorkflowID: "wf-leak@example.com", State: eventschema.WorkflowStateProgress,
			},
		},
		{
			ID: "o", Type: eventschema.EventTypeOptimization, Timestamp: time.Now().UTC(),
			Payload: &eventschema.OptimizationEvent{
				Reason: "saw key sk-proj-AbCDefGhIJKLmnopQRSTUVwxyz1234567890",
			},
		},
		{
			ID: "c", Type: eventschema.EventTypeCoaching, Timestamp: time.Now().UTC(),
			Payload: &eventschema.CoachingEvent{
				SessionID: "sess", Summary: "see token Bearer abcDEF1234567890ghijKLMN",
				ReplayMetadata: map[string]string{"actor": "bob@example.com"},
			},
		},
		{
			ID: "rs", Type: eventschema.EventTypeRuleSource, Timestamp: time.Now().UTC(),
			Payload: &eventschema.RuleSourceEvent{
				SourceID: "repo:CLAUDE.md", Path: "leak sk-proj-AbCDefGhIJKLmnopQRSTUVwxyz1234567890",
				Sections: []eventschema.RuleSection{{ID: "x", Anchor: "leak@example.com"}},
			},
		},
		{
			ID: "ra", Type: eventschema.EventTypeRuleAnalysis, Timestamp: time.Now().UTC(),
			Payload: &eventschema.RuleAnalysisEvent{
				SourceID:      "repo:CLAUDE.md",
				WorkflowID:    "wf-leak@example.com",
				ConflictsWith: []string{"sk-proj-AbCDefGhIJKLmnopQRSTUVwxyz1234567890"},
			},
		},
	}
	for _, env := range cases {
		findings := r.RedactEnvelope(env)
		if len(findings) == 0 {
			t.Errorf("no findings for %s envelope", env.Type)
		}
	}
}

func TestRedactPreservesNonSecretText(t *testing.T) {
	r := New(Config{EntropyEnabled: boolPtr(false)})
	in := "GET /v1/chat/completions HTTP/1.1\nHost: api.openai.com\nAccept: application/json"
	got, _ := r.Redact(in)
	if got != in {
		t.Errorf("non-secret text mutated:\n got: %q\nwant: %q", got, in)
	}
}

func TestEmptyInput(t *testing.T) {
	r := Default()
	got, fs := r.Redact("")
	if got != "" || fs != nil {
		t.Errorf("empty input: got %q / %+v", got, fs)
	}
	if got := r.Detect(""); got != nil {
		t.Errorf("Detect empty: got %+v, want nil", got)
	}
}

func TestPlaceholderFormat(t *testing.T) {
	if got := placeholder(KindOpenAIKey); got != "<redacted:openai_api_key>" {
		t.Errorf("placeholder = %q", got)
	}
	if got := placeholder(""); got != "<redacted:unknown>" {
		t.Errorf("placeholder default: %q", got)
	}
}

func TestShannonEntropyRanges(t *testing.T) {
	if got := shannonEntropy(""); got != 0 {
		t.Errorf("empty entropy: %f", got)
	}
	if got := shannonEntropy("aaaaaaaaaa"); got != 0 {
		t.Errorf("repeated rune entropy: %f, want 0", got)
	}
	if got := shannonEntropy("abcdefghij"); got < 3 {
		t.Errorf("varied entropy too low: %f", got)
	}
}

func boolPtr(b bool) *bool { return &b }

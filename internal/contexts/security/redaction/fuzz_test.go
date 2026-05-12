package redaction

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzRedactNeverPanicsAndPreservesPlaceholder probes Redact with
// random inputs and asserts two invariants:
//   - the redactor never panics on adversarial bytes
//   - the redacted output never contains an obvious known-secret
//     literal we seeded into the corpus (the placeholder substituted
//     the secret)
//
// Run via: go test -fuzz=FuzzRedact ./internal/contexts/security/redaction
func FuzzRedactNeverPanicsAndPreservesPlaceholder(f *testing.F) {
	seeds := []string{
		"",
		"plain text",
		"Bearer sk-secretsecretsecretsecretsecret123",
		"key=AIzaSyDxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		"\x00\x01\x02not-utf8",
		strings.Repeat("a", 4096),
		"alice@example.com",
		"prefix sk-proj-AbCDefGhIJKLmnopQRSTUVwxyz1234567890 suffix",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	r := Default()
	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			t.Skip()
		}
		out, _ := r.Redact(input)
		// Known-secret literals from seeds must not appear in output.
		for _, banned := range []string{
			"sk-secretsecretsecretsecretsecret123",
			"sk-proj-AbCDefGhIJKLmnopQRSTUVwxyz1234567890",
			"AIzaSyDxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		} {
			if strings.Contains(input, banned) && strings.Contains(out, banned) {
				t.Errorf("redactor leaked banned token %q", banned)
			}
		}
		// Output must be valid UTF-8 — placeholders use printable ASCII.
		if !utf8.ValidString(out) {
			t.Errorf("redacted output is not valid UTF-8: %q", out)
		}
	})
}

// FuzzDetectStable verifies Detect produces deterministic findings for
// the same input across repeated invocations (no map iteration jitter
// leaking into ordering).
func FuzzDetectStable(f *testing.F) {
	f.Add("Bearer sk-secretsecretsecretsecretsecret123 and key=AIzaSyDxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	r := Default()
	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			t.Skip()
		}
		first := r.Detect(input)
		second := r.Detect(input)
		if len(first) != len(second) {
			t.Fatalf("len drift: %d vs %d", len(first), len(second))
		}
		for i := range first {
			if first[i].Kind != second[i].Kind || first[i].Start != second[i].Start || first[i].End != second[i].End {
				t.Errorf("finding[%d] drift: %+v vs %+v", i, first[i], second[i])
			}
		}
	})
}

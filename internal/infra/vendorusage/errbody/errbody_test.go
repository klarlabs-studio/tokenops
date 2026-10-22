package errbody

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStatus(t *testing.T) {
	const key = "sk-ant-api03-AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJ"
	long := strings.Repeat("quota exceeded ", 100)
	cases := []struct {
		name    string
		status  int
		body    string
		want    string // exact, when set
		absent  []string
		present []string
	}{
		{name: "401 drops the body", status: 401, body: `{"echo":"Authorization: Bearer ` + key + `"}`, want: "status 401"},
		{name: "403 drops the body", status: 403, body: "cookie=abc123", want: "status 403"},
		{name: "407 drops the body", status: 407, body: "proxy credentials required for " + key, want: "status 407"},
		{name: "empty body", status: 500, body: "", want: "status 500"},
		{name: "whitespace body", status: 502, body: " \n\t ", want: "status 502"},
		{name: "short body kept", status: 500, body: `{"error":"overloaded"}`, want: `status 500: {"error":"overloaded"}`},
		{name: "secret redacted", status: 400, body: "bad key " + key, absent: []string{"sk-ant-"}, present: []string{"<redacted:anthropic_api_key>"}},
		{name: "folded onto one line", status: 500, body: "line one\r\nline two\n\nline three", want: "status 500: line one line two line three"},
		{name: "capped", status: 500, body: long, present: []string{"…"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Status(tc.status, []byte(tc.body))
			if tc.want != "" && got != tc.want {
				t.Fatalf("Status = %q; want %q", got, tc.want)
			}
			for _, a := range tc.absent {
				if strings.Contains(got, a) {
					t.Errorf("Status = %q; must not contain %q", got, a)
				}
			}
			for _, p := range tc.present {
				if !strings.Contains(got, p) {
					t.Errorf("Status = %q; want it to contain %q", got, p)
				}
			}
			if strings.ContainsAny(got, "\r\n") {
				t.Errorf("Status = %q spans lines", got)
			}
			if s := Snippet(tc.status, []byte(tc.body)); len(s) > MaxSnippetBytes+len("…") || !utf8.ValidString(s) {
				t.Errorf("Snippet is %d bytes (cap %d) or invalid UTF-8: %q", len(s), MaxSnippetBytes, s)
			}
		})
	}
}

// A cut never lands inside a multi-byte character.
func TestSnippetCutsOnRuneBoundary(t *testing.T) {
	s := Snippet(500, []byte(strings.Repeat("ä", 300)))
	if !utf8.ValidString(s) {
		t.Fatalf("invalid UTF-8: %q", s)
	}
}

// Read stops at MaxReadBytes so a huge error page is never buffered.
func TestReadIsBounded(t *testing.T) {
	got := Read(strings.NewReader(strings.Repeat("x", 10*MaxReadBytes)))
	if len(got) != MaxReadBytes {
		t.Fatalf("Read returned %d bytes; want %d", len(got), MaxReadBytes)
	}
}

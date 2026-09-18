package version

import (
	"os"
	"regexp"
	"testing"
)

// The docs site states the current version in two places, and nothing kept
// either in step with a release. The homepage advertised v0.56.0 while six
// further versions shipped — visible to every reader, checked by nobody.
//
// This compares those claims against the newest CHANGELOG entry, which is
// written as part of cutting a release and is therefore the one place that
// cannot be forgotten.
func TestDocsVersionClaimsMatchTheChangelog(t *testing.T) {
	latest := latestChangelogVersion(t)

	for _, tc := range []struct {
		file    string
		pattern string
	}{
		{"../../web/docs/index.md", `Shipping now: \*\*v([0-9]+\.[0-9]+\.[0-9]+)\*\*`},
		{"../../web/docs/changelog.md", `Current release: \*\*v([0-9]+\.[0-9]+\.[0-9]+)\*\*`},
	} {
		b, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		m := regexp.MustCompile(tc.pattern).FindStringSubmatch(string(b))
		if m == nil {
			t.Errorf("%s: no version claim matching %s — if the wording changed, update this test "+
				"rather than dropping the check", tc.file, tc.pattern)
			continue
		}
		if m[1] != latest {
			t.Errorf("%s claims v%s but the newest CHANGELOG entry is %s.\n"+
				"  Update the claim when cutting a release; readers see it on the front page.",
				tc.file, m[1], latest)
		}
	}
}

// latestChangelogVersion reads the first `## X.Y.Z` heading, which is the
// release being cut.
func latestChangelogVersion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	m := regexp.MustCompile(`(?m)^## ([0-9]+\.[0-9]+\.[0-9]+)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("no version heading in CHANGELOG.md")
	}
	return m[1]
}

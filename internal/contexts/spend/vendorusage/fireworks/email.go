package fireworks

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Bounds of the sign-in scan: Nexus CLI's directories hold its API key, so
// the scan reads little and keeps only what passes for an email.
const (
	// maxScanBytes is the largest file read; a bigger one is skipped.
	maxScanBytes = 256 << 10
	// maxScanFiles is how many JSON files under cli/ are read at most.
	maxScanFiles = 64
	// maxScanDepth is how deep below cli/ files are looked for.
	maxScanDepth = 3
	// maxJSONDepth is how deep inside a document values are looked at.
	maxJSONDepth = 6
)

var emailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+\-]{1,64}@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}$`)

// secretField matches a field name that holds, or holds a tree of,
// credentials: its value is never looked at.
var secretField = regexp.MustCompile(`(?i)key|token|secret|password|passwd|credential|cookie|auth|bearer|session`)

// identityField matches a non-email field that may name who signed in.
var identityField = regexp.MustCompile(`(?i)user|login|signed|principal|identity|account`)

// SignInEmail is the email Nexus CLI (formerly FireConnect) is signed in
// with, or "" when none is found. It reads, in order, config.json,
// key-storage.json and JSON files under cli/ in ~/.nexus, then the same in
// ~/.fireconnect. Those files can hold the API key, so only string fields
// whose name is not key-like and whose value is an email are kept, and a
// value shaped like a Fireworks key (fw_…) never is. A field named for an
// email wins over one naming a user or account.
func SignInEmail(home string) string {
	files := make([]string, 0, len(identityDirs)*2)
	for _, d := range identityDirs {
		dir := filepath.Join(home, d)
		files = append(files, filepath.Join(dir, "config.json"), filepath.Join(dir, "key-storage.json"))
		files = append(files, jsonFilesUnder(filepath.Join(dir, "cli"))...)
	}
	var fallback string
	for _, f := range files {
		named, other := emailsIn(f)
		if named != "" {
			return named
		}
		if fallback == "" {
			fallback = other
		}
	}
	return fallback
}

// jsonFilesUnder lists *.json files below dir, a few levels deep and a
// bounded number of them, in a stable order.
func jsonFilesUnder(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		depth := strings.Count(rel, string(filepath.Separator))
		if d.IsDir() {
			if path != dir && depth >= maxScanDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && strings.EqualFold(filepath.Ext(path), ".json") && len(out) < maxScanFiles {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// emailsIn reads one file and returns the first email under an
// email-named field and the first under an identity-named one.
func emailsIn(path string) (named, other string) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxScanBytes {
		return "", ""
	}
	b, err := os.ReadFile(path) //nolint:gosec // Nexus CLI's own files, bounded above
	if err != nil {
		return "", ""
	}
	var doc any
	if json.Unmarshal(b, &doc) != nil {
		return "", ""
	}
	walkEmails(doc, 0, &named, &other)
	return named, other
}

func walkEmails(v any, depth int, named, other *string) {
	if depth > maxJSONDepth {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if secretField.MatchString(k) && !strings.Contains(strings.ToLower(k), "email") {
				continue
			}
			if s, ok := t[k].(string); ok {
				if !isEmail(s) {
					continue
				}
				switch {
				case strings.Contains(strings.ToLower(k), "email"):
					if *named == "" {
						*named = s
					}
				case identityField.MatchString(k):
					if *other == "" {
						*other = s
					}
				}
				continue
			}
			walkEmails(t[k], depth+1, named, other)
		}
	case []any:
		for _, e := range t {
			walkEmails(e, depth+1, named, other)
		}
	}
}

// ValidEmail reports whether s passes for an email address the user can
// be found by: shaped like one, and not like a Fireworks key.
func ValidEmail(s string) bool { return isEmail(s) }

// isEmail is an email address that is not shaped like a Fireworks key.
func isEmail(s string) bool {
	return len(s) <= 254 && !strings.HasPrefix(strings.ToLower(s), "fw_") && emailPattern.MatchString(s)
}

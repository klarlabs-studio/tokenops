package eval

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func LoadSuite(path string) (*Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: load %s: %w", path, err)
	}
	return parseSuite(data, path)
}

// bundled holds the suites under testdata, compiled into the binary.
// Locating them on disk from the source file's path fails in every
// release: -trimpath turns that path into a Go module path.
//
//go:embed testdata/*.json
var bundled embed.FS

// BundledSuites returns the suites shipped with TokenOps, independent of
// the working directory or where the binary was built.
func BundledSuites() ([]*Suite, error) {
	names, err := fs.Glob(bundled, "testdata/*.json")
	if err != nil {
		return nil, fmt.Errorf("eval: list bundled suites: %w", err)
	}
	suites := make([]*Suite, 0, len(names))
	for _, name := range names {
		data, err := bundled.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("eval: read bundled %s: %w", name, err)
		}
		s, err := parseSuite(data, name)
		if err != nil {
			return nil, err
		}
		suites = append(suites, s)
	}
	return suites, nil
}

func parseSuite(data []byte, path string) (*Suite, error) {
	var s Suite
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("eval: parse %s: %w", path, err)
	}
	if s.Name == "" {
		s.Name = filepath.Base(path)
	}
	sortCases(&s)
	return &s, nil
}

func LoadSuites(glob string) ([]*Suite, error) {
	matches, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("eval: glob %s: %w", glob, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("eval: no suites matching %s", glob)
	}
	suites := make([]*Suite, 0, len(matches))
	for _, m := range matches {
		s, err := LoadSuite(m)
		if err != nil {
			return nil, err
		}
		suites = append(suites, s)
	}
	return suites, nil
}

func sortCases(_ *Suite) {}

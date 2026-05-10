package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func LoadSuite(path string) (*Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: load %s: %w", path, err)
	}
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

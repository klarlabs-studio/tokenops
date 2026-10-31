package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// PlanRename is a bound plan whose name the catalog retired, and the name
// it has now.
type PlanRename struct {
	Provider, From, To string
}

// renamePlans binds every plan c names by a retired name under its current
// one: Anthropic's Max 20x is Claude Ultra, and a config written before
// the rename means Ultra.
func (c *Config) renamePlans() {
	for provider, name := range c.Plans {
		if current, renamed := plans.ResolveAlias(name); renamed {
			c.Plans[provider] = current
		}
	}
}

// RenameRetiredPlans rewrites the plans the config at path binds by a
// retired name to the name each has now, and says which it rewrote. Only
// those values change: comments, order and layout are kept as they are,
// byte for byte. A missing file has nothing to rename.
func RenameRetiredPlans(path string) ([]PlanRename, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the operator's own config
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	var out []PlanRename
	for _, kv := range planEntries(&doc) {
		key, val := kv[0], kv[1]
		current, renamed := plans.ResolveAlias(val.Value)
		if !renamed || val.Kind != yaml.ScalarNode || val.Line < 1 || val.Line > len(lines) {
			continue
		}
		line := lines[val.Line-1]
		at := bytes.Index(line[min(val.Column-1, len(line)):], []byte(val.Value))
		if at < 0 {
			continue
		}
		at += min(val.Column-1, len(line))
		lines[val.Line-1] = append(append(append([]byte{}, line[:at]...), current...), line[at+len(val.Value):]...)
		out = append(out, PlanRename{Provider: key.Value, From: val.Value, To: current})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, writeAtomic(path, bytes.Join(lines, nil))
}

// planEntries are the key and value nodes of the top-level plans mapping.
func planEntries(doc *yaml.Node) [][2]*yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "plans" || root.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		m := root.Content[i+1]
		var out [][2]*yaml.Node
		for j := 0; j+1 < len(m.Content); j += 2 {
			out = append(out, [2]*yaml.Node{m.Content[j], m.Content[j+1]})
		}
		return out
	}
	return nil
}

// writeAtomic replaces path with data at the config's permissions.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), ConfigFilePerm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

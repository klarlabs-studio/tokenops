package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ChangedError is a write refused because the file changed, after the
// config was read, in the same place the write changes. Nothing was
// written; reading again and repeating the change settles it.
type ChangedError struct {
	Path string
	// Keys are the dotted keys both sides changed.
	Keys []string
}

func (e *ChangedError) Error() string {
	return fmt.Sprintf("%s changed while this change was being made (%s), so nothing was written; run it again",
		e.Path, strings.Join(e.Keys, ", "))
}

// mergeChange applies a change to the config file as it is now.
//
// read is the file as the caller read it, mine the caller's config after
// its change, and current the file on disk. What differs between the
// config as read and mine is the change, and only that is written into
// current: a key the caller did not touch keeps what the file holds,
// whether another process changed it since, this binary does not know it,
// or the operator wrote a comment beside it.
//
// Mappings are merged key by key. A list or a single value is one unit:
// when the file changed it since the read, to something other than what
// the caller wants, the change is refused with a ChangedError instead of
// one side being dropped.
func mergeChange(path string, read, mine, current []byte) ([]byte, error) {
	asRead, err := decodeMutable(read)
	if err != nil {
		return nil, err
	}
	base, err := yaml.Marshal(asRead)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var baseDoc, mineDoc, readDoc, currentDoc yaml.Node
	for _, d := range []struct {
		data []byte
		doc  *yaml.Node
	}{{base, &baseDoc}, {mine, &mineDoc}, {read, &readDoc}, {current, &currentDoc}} {
		if err := yaml.Unmarshal(d.data, d.doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	root := rootMapping(&currentDoc)
	if root == nil {
		return nil, fmt.Errorf("%s is no longer a mapping of settings, so the change was not written", path)
	}
	var conflicts []string
	patchMapping(root, rootMapping(&baseDoc), rootMapping(&mineDoc), rootMapping(&readDoc), "", &conflicts)
	if len(conflicts) > 0 {
		return nil, &ChangedError{Path: path, Keys: conflicts}
	}
	out, err := yaml.Marshal(&currentDoc)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	// Each side was valid alone; together they have to be as well.
	merged, err := decodeMutable(out)
	if err != nil {
		return nil, err
	}
	if err := merged.Validate(); err != nil {
		return nil, fmt.Errorf("%s changed while this change was being made, and the two do not fit together: %w", path, err)
	}
	return out, nil
}

// rootMapping is the document's top-level mapping. An empty document gets
// one; a document holding anything else has none.
func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if doc.Kind != yaml.DocumentNode {
		return nil
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if root := doc.Content[0]; root.Kind == yaml.MappingNode {
		return root
	}
	return nil
}

// patchMapping writes into theirs every key whose value differs between
// base and mine. read is the same mapping as the caller read it, which
// says whether theirs changed since; it is nil where the file had none.
func patchMapping(theirs, base, mine, read *yaml.Node, path string, conflicts *[]string) {
	for _, key := range mappingKeys(base, mine) {
		b, m := mappingValue(base, key), mappingValue(mine, key)
		if sameNode(b, m) {
			continue
		}
		t, r := mappingValue(theirs, key), mappingValue(read, key)
		at := key
		if path != "" {
			at = path + "." + key
		}
		// A mapping on either side is merged key by key. One that is
		// missing on the other side (a section left out when empty) counts
		// as empty there, so taking the last key out of a section does not
		// take out a key somebody else put in.
		if (isMapping(b) || isMapping(m)) && (b == nil || isMapping(b)) && (m == nil || isMapping(m)) {
			// A section the file leaves out, or leaves empty, gets only
			// the keys that changed: the rest are the defaults they were.
			added := t == nil || isNull(t)
			if added {
				t = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				setMappingValue(theirs, key, t)
			}
			if isMapping(t) {
				patchMapping(t, b, m, asMapping(r), at, conflicts)
				if len(t.Content) == 0 && (added || m == nil) {
					removeMappingKey(theirs, key)
				}
				continue
			}
		}
		if !sameNode(t, r) && !sameNode(t, m) {
			*conflicts = append(*conflicts, at)
			continue
		}
		if m == nil {
			removeMappingKey(theirs, key)
			continue
		}
		setMappingValue(theirs, key, m)
	}
}

// mappingKeys are base's keys, then the ones only mine has.
func mappingKeys(base, mine *yaml.Node) []string {
	var keys []string
	seen := map[string]bool{}
	for _, m := range []*yaml.Node{base, mine} {
		if !isMapping(m) {
			continue
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			if k := m.Content[i].Value; !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// mappingValue is the value m holds under key, nil when it holds none.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if !isMapping(m) {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setMappingValue puts value under key, in the key's place when m has it
// and at the end when not. A comment on the line of the value replaced
// stays on that line.
func setMappingValue(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		if old := m.Content[i+1]; old.Kind == yaml.ScalarNode && value.Kind == yaml.ScalarNode && value.LineComment == "" {
			value.LineComment = old.LineComment
		}
		m.Content[i+1] = value
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func removeMappingKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func isMapping(n *yaml.Node) bool { return n != nil && n.Kind == yaml.MappingNode }

func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null" }

func asMapping(n *yaml.Node) *yaml.Node {
	if isMapping(n) {
		return n
	}
	return nil
}

// sameNode reports whether a and b hold the same value, however each is
// written: quoting, layout, comments and the order of a mapping's keys do
// not count.
func sameNode(a, b *yaml.Node) bool {
	a, b = aliased(a), aliased(b)
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case yaml.ScalarNode:
		return a.ShortTag() == b.ShortTag() && a.Value == b.Value
	case yaml.SequenceNode:
		if len(a.Content) != len(b.Content) {
			return false
		}
		for i := range a.Content {
			if !sameNode(a.Content[i], b.Content[i]) {
				return false
			}
		}
		return true
	case yaml.MappingNode:
		if len(a.Content) != len(b.Content) {
			return false
		}
		for i := 0; i+1 < len(a.Content); i += 2 {
			other := mappingValue(b, a.Content[i].Value)
			if other == nil || !sameNode(a.Content[i+1], other) {
				return false
			}
		}
		return true
	}
	return false
}

// aliased is the node an alias stands for, and n itself otherwise.
func aliased(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

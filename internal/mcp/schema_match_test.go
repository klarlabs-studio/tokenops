package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"go.klarlabs.de/mcp/schema"
)

// assertMatchesOutputSchema checks out against the schema a tool
// declares for it, the way a strict MCP client does.
//
// The schema package's own validator does not enforce
// additionalProperties, which is exactly the check Claude Code fails a
// call on ("must NOT have additional properties"), so this walks the
// closed objects itself as well.
func assertMatchesOutputSchema(t *testing.T, declared, out any) {
	t.Helper()
	s, err := schema.Generate(declared)
	if err != nil {
		t.Fatalf("schema.Generate: %v", err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	if err := s.Validate(raw); err != nil {
		t.Errorf("output does not match its schema: %v", err)
	}
	var sm, value any
	sb, _ := json.Marshal(s)
	_ = json.Unmarshal(sb, &sm)
	_ = json.Unmarshal(raw, &value)
	for _, extra := range closedObjectExtras("data", sm, value) {
		t.Errorf("%s must NOT have additional properties", extra)
	}
}

// closedObjectExtras lists the properties value carries that a closed
// object in the schema does not declare.
func closedObjectExtras(path string, sch, value any) []string {
	sm, ok := sch.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	switch v := value.(type) {
	case map[string]any:
		props, _ := sm["properties"].(map[string]any)
		closed := sm["additionalProperties"] == false
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sub, declared := props[k]
			if !declared {
				if closed {
					out = append(out, fmt.Sprintf("%s/%s", path, k))
				}
				continue
			}
			out = append(out, closedObjectExtras(path+"/"+k, sub, v[k])...)
		}
	case []any:
		for i, item := range v {
			out = append(out, closedObjectExtras(fmt.Sprintf("%s/%d", path, i), sm["items"], item)...)
		}
	}
	return out
}

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckJSONScalar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "response.json")
	if err := os.WriteFile(path, []byte(`{"choices":[{"message":{"content":"TOKENOPS_OK"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	matched, err := checkJSONScalar(path, "/choices/0/message/content", "TOKENOPS_OK")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("expected selected scalar to match")
	}
	matched, err = checkJSONScalar(path, "/choices/0/message/content", "other")
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("expected selected scalar not to match")
	}
}

func TestResolveJSONPointerEscapesAndRejectsContainers(t *testing.T) {
	value := map[string]any{"a/b": map[string]any{"~key": "value"}}
	got, err := resolveJSONPointer(value, "/a~1b/~0key")
	if err != nil || got != "value" {
		t.Fatalf("resolve escaped pointer = %v, %v", got, err)
	}
	if _, err := jsonScalarString(value); err == nil {
		t.Fatal("expected container value to be rejected")
	}
}

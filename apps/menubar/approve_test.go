package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApprovalOpensATerminalThatAsks(t *testing.T) {
	var opened string
	prev := openFile
	openFile = func(path string) error { opened = path; return nil }
	t.Cleanup(func() { openFile = prev })

	if err := openApproval("bnd-0123456789ab", true); err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(opened) != ".command" {
		t.Fatalf("opened %q", opened)
	}
	b, err := os.ReadFile(opened)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "tokenops approve bnd-0123456789ab --confirm --session\n") {
		t.Fatalf("script:\n%s", b)
	}
	if info, _ := os.Stat(opened); info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("script is not executable: %v", info.Mode())
	}
}

func TestApprovalRefusesWhatIsNotADecisionID(t *testing.T) {
	prev := openFile
	openFile = func(string) error { t.Fatal("opened a terminal for a bad id"); return nil }
	t.Cleanup(func() { openFile = prev })
	for _, id := range []string{"", "bnd-xyz", "bnd-0123456789ab; rm -rf ~", "bnd-0123456789AB"} {
		if err := openApproval(id, false); err == nil {
			t.Errorf("%q was accepted", id)
		}
	}
}

func TestDenyGoesThroughTheAPIAndApprovalNever(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/boundaries/decisions" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	d := &daemon{urlFile: filepath.Join(t.TempDir(), "daemon.url"), hc: srv.Client()}
	if err := os.WriteFile(d.urlFile, []byte(`{"url":"`+srv.URL+`","dashboard_token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.denyBoundary(context.Background(), "bnd-0123456789ab", false); err != nil {
		t.Fatal(err)
	}
	if got["decision"] != "deny" || got["id"] != "bnd-0123456789ab" {
		t.Fatalf("sent %v", got)
	}
}

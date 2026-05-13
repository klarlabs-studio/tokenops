package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func executeRoot(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	cmd := NewRoot()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	err = cmd.Execute()
	return outBuf.String(), err
}

func TestRootHelpListsSubcommands(t *testing.T) {
	out, err := executeRoot(t, "--help")
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, want := range []string{"start", "status", "version", "config"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q\n%s", want, out)
		}
	}
}

func TestVersionSubcommand(t *testing.T) {
	out, err := executeRoot(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.HasPrefix(out, "tokenops ") {
		t.Errorf("output = %q, want prefix 'tokenops '", out)
	}
}

func TestConfigShowYAML(t *testing.T) {
	t.Setenv("TOKENOPS_LISTEN", "127.0.0.1:9999")
	out, err := executeRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(out, "listen: 127.0.0.1:9999") {
		t.Errorf("yaml missing listen override:\n%s", out)
	}
}

func TestConfigShowJSON(t *testing.T) {
	out, err := executeRoot(t, "config", "show", "--json")
	if err != nil {
		t.Fatalf("config show --json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if _, ok := got["Listen"]; !ok {
		t.Errorf("Listen key missing in: %v", got)
	}
}

func TestConfigShowReadsFileFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:9001\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out, err := executeRoot(t, "--config", path, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(out, "127.0.0.1:9001") {
		t.Errorf("file-loaded listen missing:\n%s", out)
	}
}

func TestFlagOverridesWinOverEnv(t *testing.T) {
	t.Setenv("TOKENOPS_LISTEN", "127.0.0.1:1111")
	out, err := executeRoot(t, "--listen", "127.0.0.1:2222", "config", "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(out, "127.0.0.1:2222") {
		t.Errorf("flag override lost:\n%s", out)
	}
}

func TestInvalidLogLevelFails(t *testing.T) {
	_, err := executeRoot(t, "--log-level", "verbose", "config", "show")
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "log level") {
		t.Errorf("error = %q, want mention of log level", err)
	}
}

// fakeDoer is a tiny httpDoer that returns canned responses keyed by URL
// path so status tests can run without binding a socket.
type fakeDoer struct {
	responses map[string]fakeResponse
	netErr    error
}

type fakeResponse struct {
	status int
	body   string
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	if f.netErr != nil {
		return nil, f.netErr
	}
	r, ok := f.responses[req.URL.Path]
	if !ok {
		return nil, errors.New("no fake response for " + req.URL.Path)
	}
	return &http.Response{
		StatusCode: r.status,
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    req,
	}, nil
}

func TestStatusJSON(t *testing.T) {
	prev := statusClient
	t.Cleanup(func() { SetStatusClient(prev) })
	SetStatusClient(&fakeDoer{responses: map[string]fakeResponse{
		"/healthz": {status: 200, body: `{"status":"ok"}`},
		"/readyz":  {status: 200, body: `{"status":"ready"}`},
		"/version": {status: 200, body: `{"version":"dev"}`},
	}})
	out, err := executeRoot(t, "status", "--addr", "127.0.0.1:7878", "--json")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var got statusResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	if got.Health.Status != 200 || got.Health.Body["status"] != "ok" {
		t.Errorf("health body unexpected: %+v", got.Health)
	}
	if got.Ready.Status != 200 || got.Version.Status != 200 {
		t.Errorf("non-200 in: %+v / %+v", got.Ready, got.Version)
	}
}

func TestStatusTextFormat(t *testing.T) {
	prev := statusClient
	t.Cleanup(func() { SetStatusClient(prev) })
	SetStatusClient(&fakeDoer{responses: map[string]fakeResponse{
		"/healthz": {status: 200, body: `{"status":"ok"}`},
		"/readyz":  {status: 503, body: `{"status":"not_ready"}`},
		"/version": {status: 200, body: `{"version":"dev"}`},
	}})
	out, err := executeRoot(t, "status", "--addr", "127.0.0.1:7878")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"daemon: http://127.0.0.1:7878", "health", "ready", "503", "version"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestStatusFallsBackWhenDaemonUnreachable(t *testing.T) {
	prev := statusClient
	t.Cleanup(func() { SetStatusClient(prev) })
	SetStatusClient(&fakeDoer{netErr: errors.New("connection refused")})

	out, err := executeRoot(t, "status", "--addr", "127.0.0.1:1")
	if err != nil {
		t.Fatalf("offline fallback should succeed, got %v", err)
	}
	if !strings.Contains(out, "not running") {
		t.Errorf("output should explain daemon is offline: %s", out)
	}
	if !strings.Contains(out, "tokenops start") {
		t.Errorf("output should suggest `tokenops start`: %s", out)
	}
}

func TestStatusAgainstRealServer(t *testing.T) {
	// Sanity check: real httptest server, default statusClient, single call.
	prev := statusClient
	t.Cleanup(func() { SetStatusClient(prev) })
	SetStatusClient(&http.Client{})

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ready"}`)
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":"test"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	addr := strings.TrimPrefix(srv.URL, "http://")
	out, err := executeRoot(t, "status", "--addr", addr, "--json")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, `"status":200`) {
		t.Errorf("expected 200 statuses in:\n%s", out)
	}
}

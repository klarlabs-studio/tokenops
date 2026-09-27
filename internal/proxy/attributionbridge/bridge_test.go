package attributionbridge

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHandlerAddsExecutionAndWorkflowIdentityAndPreservesRequest(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotExecutionID, gotWorkflowID, gotAPIKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		gotExecutionID, gotAPIKey = r.Header.Get(executionHeader), r.Header.Get("x-api-key")
		gotWorkflowID = r.Header.Get(workflowHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handler, err := NewHandler(upstream.URL, "execution:operator-selected", "workflow:prepared")
	if err != nil {
		t.Fatal(err)
	}
	bridge := httptest.NewServer(handler)
	defer bridge.Close()

	req, err := http.NewRequest(http.MethodPost, bridge.URL+"/anthropic/v1/messages?beta=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", "test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if gotMethod != http.MethodPost || gotPath != "/anthropic/v1/messages" || gotQuery != "beta=1" {
		t.Fatalf("forwarded request = %s %s?%s", gotMethod, gotPath, gotQuery)
	}
	if gotExecutionID != "execution:operator-selected" || gotAPIKey != "test-key" {
		t.Fatalf("headers = execution %q, API key %q", gotExecutionID, gotAPIKey)
	}
	if gotWorkflowID != "workflow:prepared" {
		t.Fatalf("workflow header = %q", gotWorkflowID)
	}
}

func TestNewHandlerRejectsUnsafeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, target, executionID, workflowID string
	}{
		{name: "relative target", target: "/anthropic", executionID: "exec"},
		{name: "target with path", target: "http://127.0.0.1:7878/anthropic", executionID: "exec"},
		{name: "remote target", target: "https://api.anthropic.com", executionID: "exec"},
		{name: "empty execution", target: "http://127.0.0.1:7878", executionID: ""},
		{name: "header injection", target: "http://127.0.0.1:7878", executionID: "exec\r\nX-Evil: yes"},
		{name: "workflow header injection", target: "http://127.0.0.1:7878", executionID: "exec", workflowID: "wf\r\nX-Evil: yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewHandler(tc.target, tc.executionID, tc.workflowID); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}

func TestHandlerRejectsNonAnthropicPaths(t *testing.T) {
	handler, err := NewHandler("http://127.0.0.1:7878", "exec", "")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

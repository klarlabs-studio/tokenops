package proxy

import (
	"bytes"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.klarlabs.de/tokenops/internal/proxy/cache"
)

// largeBodyUpstream records the SHA-256 of every request body it receives.
func largeBodyUpstream(t *testing.T) (*httptest.Server, *atomic.Pointer[[32]byte]) {
	t.Helper()
	var got atomic.Pointer[[32]byte]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sum := sha256.Sum256(body)
		got.Store(&sum)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-4o","usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// A request body larger than the observer's capture limit must reach the
// upstream byte for byte. Observation may degrade; the request must not.
func TestProxyForwardsBodiesLargerThanCaptureLimit(t *testing.T) {
	body := bytes.Repeat([]byte("x"), maxRequestBodyCapture+512*1024)
	want := sha256.Sum256(body)

	for _, tc := range []struct {
		name    string
		chunked bool
		start   func(t *testing.T, upstream *httptest.Server) string
	}{
		{name: "observer, content-length", start: func(t *testing.T, up *httptest.Server) string {
			base, _ := startProxyForObservation(t, up)
			return base
		}},
		{name: "observer, chunked", chunked: true, start: func(t *testing.T, up *httptest.Server) string {
			base, _ := startProxyForObservation(t, up)
			return base
		}},
		{name: "cache", start: func(t *testing.T, up *httptest.Server) string {
			return "http://" + startCacheProxy(t, up.URL, cache.New(cache.Options{}))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream, got := largeBodyUpstream(t)
			base := tc.start(t, upstream)
			var reqBody io.Reader = bytes.NewReader(body)
			if tc.chunked {
				reqBody = io.MultiReader(reqBody) // hides the length, forcing chunked encoding
			}
			req, err := http.NewRequest(http.MethodPost, base+"/openai/v1/chat/completions", reqBody)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if sum := got.Load(); sum == nil || *sum != want {
				t.Fatal("upstream did not receive the full request body")
			}
		})
	}
}

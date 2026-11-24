package accounts

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveHeaders answers body at path when every header given is sent as
// given, and 401 otherwise.
func serveHeaders(t *testing.T, path string, headers map[string]string, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			if r.Header.Get(k) != v {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

package accounts

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// webServer is a vendor console that answers only requests carrying the
// session cookie it issued: every other request is refused with status
// refuse (401, or a 302 to a sign-in page). routes map a path to its body;
// a path with no route is 404. It records each request's path and body.
type webServer struct {
	*httptest.Server
	mu     sync.Mutex
	paths  []string
	bodies []string
}

func newWebServer(t *testing.T, session string, refuse int, routes map[string]string) *webServer {
	t.Helper()
	w := &webServer{}
	w.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.mu.Lock()
		w.paths, w.bodies = append(w.paths, r.URL.Path), append(w.bodies, string(b))
		w.mu.Unlock()
		if session != "" && !strings.Contains(r.Header.Get("Cookie"), session) {
			if refuse == http.StatusFound {
				rw.Header().Set("Location", "https://signin.example/login")
			}
			rw.WriteHeader(refuse)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = rw.Write([]byte(body))
	}))
	t.Cleanup(w.Close)
	return w
}

// requests is how many requests reached the server.
func (w *webServer) requests() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.paths)
}

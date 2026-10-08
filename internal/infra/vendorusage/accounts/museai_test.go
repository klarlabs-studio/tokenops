package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const (
	museAICookie = "hatch_sess=hs"
	museAIID     = "40447f7d32b58c622904d92f8ac01a13f4798568bd"
)

// museAIServer is muse.ai: the page and its chunks, and the server action
// answering only the current action ID with the session (a stale ID is
// muse.ai's 404). It counts the page loads.
type museAIServer struct {
	*httptest.Server
	mu             sync.Mutex
	pageLoads      int
	chunkHadCookie bool
}

func newMuseAIServer(t *testing.T, answer string) *museAIServer {
	t.Helper()
	routes := routesOf(t, "museai")
	if answer != "" {
		routes["POST /"] = answer
	}
	s := &museAIServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/_next/") {
			s.chunkHadCookie = s.chunkHadCookie || r.Header.Get("Cookie") != ""
		} else if !strings.Contains(r.Header.Get("Cookie"), museAICookie) {
			w.Header().Set("Location", "https://auth.muse.ai/aymh/?origin=x")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			s.pageLoads++
		}
		if r.Method == http.MethodPost {
			if r.Header.Get("Next-Action") != museAIID || r.Header.Get("Sec-Fetch-Mode") != "cors" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("Server action not found."))
				return
			}
		}
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func forgetMuseAIAction() {
	museAIAction.Lock()
	museAIAction.id = map[string]string{}
	museAIAction.Unlock()
}

func TestMuseAIFindsTheActionAndReadsTheWeek(t *testing.T) {
	forgetMuseAIAction()
	srv := newMuseAIServer(t, "")
	got, err := MuseAI{BaseURL: srv.URL}.Read(context.Background(), museAICookie)
	if err != nil || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "week" || w.UsedPct != 3 || w.Duration != 7*24*time.Hour || !w.ResetsAt.Equal(time.Unix(1790634838, 0)) {
		t.Errorf("week %+v", w)
	}
	if srv.chunkHadCookie {
		t.Error("the session was sent with a script chunk")
	}
	// The action ID is kept: a second read does not load the page again.
	if _, err := (MuseAI{BaseURL: srv.URL}).Read(context.Background(), museAICookie); err != nil || srv.pageLoads != 1 {
		t.Errorf("second read: %v after %d page loads", err, srv.pageLoads)
	}
}

// A stale action ID (muse.ai redeployed) is found again.
func TestMuseAIRediscoversAStaleAction(t *testing.T) {
	forgetMuseAIAction()
	srv := newMuseAIServer(t, "")
	museAIAction.Lock()
	museAIAction.id[srv.URL] = "0000000000000000000000000000000000000000aa"
	museAIAction.Unlock()
	got, err := MuseAI{BaseURL: srv.URL}.Read(context.Background(), museAICookie)
	if err != nil || len(got.Windows) != 1 || srv.pageLoads != 1 {
		t.Fatalf("got %+v, %v after %d page loads", got, err, srv.pageLoads)
	}
}

func TestMuseAIFreePlan(t *testing.T) {
	forgetMuseAIAction()
	srv := newMuseAIServer(t, `1:{"success":true,"subscription":{"tier":{"name":"Muse Free"},"usage":{"percentUsed":28,"resetsAt":1790634838},"usageRowValueLabel":"28% used","agreement":null}}`)
	got, err := MuseAI{BaseURL: srv.URL}.Read(context.Background(), museAICookie)
	if err != nil || got.Windows[0].UsedPct != 28 {
		t.Errorf("free = %+v, %v", got, err)
	}
}

func TestMuseAIExpiredSession(t *testing.T) {
	forgetMuseAIAction()
	srv := newMuseAIServer(t, "")
	if _, err := (MuseAI{BaseURL: srv.URL}).Read(context.Background(), "hatch_sess=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	if _, err := (MuseAI{BaseURL: srv.URL}).Read(context.Background(), "other=x"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("no hatch_sess = %v", err)
	}
}

func TestMuseAIUnknownAnswer(t *testing.T) {
	forgetMuseAIAction()
	srv := newMuseAIServer(t, `1:{"success":true,"subscription":{"usage":{"percentUsed":"3"}}}`)
	if _, err := (MuseAI{BaseURL: srv.URL}).Read(context.Background(), museAICookie); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("unknown answer = %v", err)
	}
}

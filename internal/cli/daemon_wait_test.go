package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type healthSequenceDoer struct {
	statuses []int
	body     string
	calls    int
}

func (d *healthSequenceDoer) Do(req *http.Request) (*http.Response, error) {
	if len(d.statuses) == 0 {
		return nil, errors.New("connection refused")
	}
	i := d.calls
	if i >= len(d.statuses) {
		i = len(d.statuses) - 1
	}
	d.calls++
	body := d.body
	if body == "" {
		body = `{"status":"ok"}`
	}
	return &http.Response{
		StatusCode: d.statuses[i],
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestWaitForDaemonHealthRetriesUntilHealthy(t *testing.T) {
	prev := statusClient
	doer := &healthSequenceDoer{statuses: []int{http.StatusServiceUnavailable, http.StatusOK}}
	SetStatusClient(doer)
	t.Cleanup(func() { SetStatusClient(prev) })

	warming, err := waitForDaemonHealth(context.Background(), "http://127.0.0.1:7878", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if warming {
		t.Error("a daemon answering {\"status\":\"ok\"} reported as warming up")
	}
	if doer.calls != 2 {
		t.Fatalf("health calls = %d, want 2", doer.calls)
	}
}

func TestWaitForDaemonHealthIsBounded(t *testing.T) {
	prev := statusClient
	SetStatusClient(&fakeDoer{netErr: errors.New("connection refused")})
	t.Cleanup(func() { SetStatusClient(prev) })

	started := time.Now()
	_, err := waitForDaemonHealth(context.Background(), "http://127.0.0.1:7878", 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("wait error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded wait took %s", elapsed)
	}
}

// A daemon answers its health check from the first moments of its boot,
// saying it is starting while it opens its store. That is a daemon that
// started, not one that failed: `daemon restart` reported a normal boot
// on a large store as "did not become healthy".
func TestWaitForDaemonHealthReportsWarmingUp(t *testing.T) {
	prev := statusClient
	SetStatusClient(&healthSequenceDoer{statuses: []int{http.StatusOK}, body: `{"status":"ok","state":"starting"}`})
	t.Cleanup(func() { SetStatusClient(prev) })

	warming, err := waitForDaemonHealth(context.Background(), "http://127.0.0.1:7878", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !warming {
		t.Error("a starting daemon was not reported as warming up")
	}
}

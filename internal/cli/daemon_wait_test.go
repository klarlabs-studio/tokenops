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
	return &http.Response{
		StatusCode: d.statuses[i],
		Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
		Request:    req,
	}, nil
}

func TestWaitForDaemonHealthRetriesUntilHealthy(t *testing.T) {
	prev := statusClient
	doer := &healthSequenceDoer{statuses: []int{http.StatusServiceUnavailable, http.StatusOK}}
	SetStatusClient(doer)
	t.Cleanup(func() { SetStatusClient(prev) })

	if err := waitForDaemonHealth(context.Background(), "http://127.0.0.1:7878", time.Second); err != nil {
		t.Fatal(err)
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
	err := waitForDaemonHealth(context.Background(), "http://127.0.0.1:7878", 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("wait error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded wait took %s", elapsed)
	}
}

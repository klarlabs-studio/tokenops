package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/tlsmint"
)

func TestServerTLSHandshake(t *testing.T) {
	bundle, err := tlsmint.EnsureBundle(t.TempDir(), tlsmint.Options{})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithTLS(bundle.TLSConfig()),
	)
	if !srv.TLSEnabled() {
		t.Fatal("TLSEnabled = false after WithTLS")
	}
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    bundle.CAPool(),
				MinVersion: tls.VersionTLS12,
			},
		},
		Timeout: 3 * time.Second,
	}
	url := "https://" + srv.Addr() + "/healthz"

	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		resp, err = client.Get(url)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("https GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestPlainHTTPRejectedByTLSServer(t *testing.T) {
	bundle, err := tlsmint.EnsureBundle(t.TempDir(), tlsmint.Options{})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithTLS(bundle.TLSConfig()),
	)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})

	// Wait until the listener is up by trying a TLS handshake.
	deadline := time.Now().Add(2 * time.Second)
	tlsClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: bundle.CAPool(), MinVersion: tls.VersionTLS12,
		}},
		Timeout: time.Second,
	}
	for time.Now().Before(deadline) {
		if resp, err := tlsClient.Get("https://" + srv.Addr() + "/healthz"); err == nil {
			_ = resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	plain := &http.Client{Timeout: time.Second}
	resp, err := plain.Get("http://" + srv.Addr() + "/healthz")
	if err == nil {
		_ = resp.Body.Close()
		// http.Server speaks TLS only; an HTTP/1.1 GET sent in the clear
		// over a TLS listener manifests as a 400 with "Client sent an
		// HTTP request to an HTTPS server" or as a transport error. Both
		// are acceptable; what we must NOT see is 200.
		if resp.StatusCode == 200 {
			t.Errorf("plain HTTP got 200 against TLS server")
		}
	}
}

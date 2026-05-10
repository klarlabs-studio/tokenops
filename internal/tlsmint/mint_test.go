package tlsmint

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureBundleMintsAndPersists(t *testing.T) {
	dir := t.TempDir()
	b, err := EnsureBundle(dir, Options{})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if b.LeafCert == nil || b.CACert == nil {
		t.Fatal("nil certs")
	}
	for _, name := range []string{CAKeyFile, CACertFile, LeafKeyFile, LeafCert} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
		if name == CAKeyFile || name == LeafKeyFile {
			if fi.Mode().Perm() != 0o600 {
				t.Errorf("%s perm = %o, want 0600", name, fi.Mode().Perm())
			}
		}
	}
}

func TestEnsureBundleIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	b1, err := EnsureBundle(dir, Options{})
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	b2, err := EnsureBundle(dir, Options{})
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if b1.LeafCert.SerialNumber.Cmp(b2.LeafCert.SerialNumber) != 0 {
		t.Errorf("re-ensure regenerated leaf (serials differ)")
	}
}

func TestLoadBundleMissing(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadBundle(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

func TestLeafIncludesLoopbackSANs(t *testing.T) {
	b, err := EnsureBundle(t.TempDir(), Options{Hostnames: []string{"tokenops.local"}})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	dnsHas := func(name string) bool {
		for _, n := range b.LeafCert.DNSNames {
			if n == name {
				return true
			}
		}
		return false
	}
	if !dnsHas("localhost") {
		t.Errorf("localhost missing from DNS SANs: %v", b.LeafCert.DNSNames)
	}
	if !dnsHas("tokenops.local") {
		t.Errorf("custom DNS SAN missing: %v", b.LeafCert.DNSNames)
	}
	ipHas := func(ip net.IP) bool {
		for _, x := range b.LeafCert.IPAddresses {
			if x.Equal(ip) {
				return true
			}
		}
		return false
	}
	if !ipHas(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("127.0.0.1 missing from IP SANs: %v", b.LeafCert.IPAddresses)
	}
}

func TestLeafIsCASignedAndVerifies(t *testing.T) {
	b, err := EnsureBundle(t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	pool := b.CAPool()
	_, err = b.LeafCert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSName:   "localhost",
	})
	if err != nil {
		t.Errorf("leaf verify: %v", err)
	}
}

func TestEndToEndHTTPSHandshake(t *testing.T) {
	b, err := EnsureBundle(t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	srv := &http.Server{
		TLSConfig:         b.TLSConfig(),
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}),
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() {
		_ = srv.ServeTLS(ln, "", "")
	}()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: b.CAPool(), MinVersion: tls.VersionTLS12},
		},
		Timeout: 5 * time.Second,
	}
	url := "https://" + ln.Addr().String()

	// Retry briefly until the goroutine accepted Serve.
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
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "ok" {
		t.Errorf("body = %q", body)
	}
}

func TestCAPoolVerifiesLeaf(t *testing.T) {
	b, err := EnsureBundle(t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := b.LeafCert.Verify(x509.VerifyOptions{
		Roots:     b.CAPool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSName:   "localhost",
	}); err != nil {
		t.Errorf("verify with CAPool: %v", err)
	}
}

func TestEmptyDirRejected(t *testing.T) {
	if _, err := EnsureBundle("", Options{}); err == nil {
		t.Fatal("expected error for empty dir")
	}
}

// Package tlsmint mints and persists a self-signed TLS bundle (CA + leaf)
// for the local TokenOps proxy. The bundle is written once per host on
// first run and reused on subsequent boots so client trust configuration
// (e.g. importing the CA into the OS trust store) survives restarts.
//
// The bundle is intentionally local-only: the leaf certificate is issued
// for loopback hostnames (localhost, 127.0.0.1, ::1) and any extra SANs
// provided by the caller. Users who want to expose the proxy beyond the
// host should regenerate with their own SANs or front the daemon with a
// real CA-issued certificate.
package tlsmint

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// File names within the cert directory. Stable identifiers so external
// tooling (OS trust import scripts, browser config) can rely on them.
const (
	CAKeyFile   = "ca.key"
	CACertFile  = "ca.crt"
	LeafKeyFile = "tls.key"
	LeafCert    = "tls.crt"
)

// Bundle is a loaded or freshly minted TLS bundle.
type Bundle struct {
	// Dir is the directory the bundle lives in.
	Dir string
	// CACert is the CA certificate (PEM-encoded on disk).
	CACert *x509.Certificate
	// LeafCert is the server leaf certificate.
	LeafCert *x509.Certificate
	// TLSCertificate is the leaf cert + key ready to plug into tls.Config.
	TLSCertificate tls.Certificate
}

// Options tune EnsureBundle. Zero values produce sensible defaults: the
// CA is valid for 10 years, the leaf for 1 year, and the leaf SANs cover
// loopback hostnames.
type Options struct {
	// Hostnames are added as DNS SANs on the leaf cert. Loopback hosts
	// are included automatically; pass nil for the default set.
	Hostnames []string
	// IPAddresses are added as IP SANs on the leaf cert. 127.0.0.1 and
	// ::1 are included automatically.
	IPAddresses []net.IP
	// CAValidFor is the CA validity window. Default 10 years.
	CAValidFor time.Duration
	// LeafValidFor is the leaf validity window. Default 1 year.
	LeafValidFor time.Duration
	// Now overrides time.Now during cert minting (for tests).
	Now func() time.Time
}

// EnsureBundle loads the bundle from dir if it exists, otherwise mints a
// fresh CA + leaf and writes both to disk. The function is idempotent:
// repeated calls on the same dir return the same bundle.
func EnsureBundle(dir string, opts Options) (*Bundle, error) {
	if dir == "" {
		return nil, errors.New("tlsmint: cert dir must not be empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("tlsmint: create cert dir: %w", err)
	}
	if b, ok, err := load(dir); err != nil {
		return nil, err
	} else if ok {
		return b, nil
	}
	return mint(dir, opts)
}

// LoadBundle reads an existing bundle from dir. Returns os.ErrNotExist if
// the bundle is incomplete; callers can then fall back to EnsureBundle.
func LoadBundle(dir string) (*Bundle, error) {
	b, ok, err := load(dir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, os.ErrNotExist
	}
	return b, nil
}

func load(dir string) (*Bundle, bool, error) {
	caPath := filepath.Join(dir, CACertFile)
	leafCertPath := filepath.Join(dir, LeafCert)
	leafKeyPath := filepath.Join(dir, LeafKeyFile)
	for _, p := range []string{caPath, leafCertPath, leafKeyPath} {
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("tlsmint: stat %s: %w", p, err)
		}
	}

	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, false, fmt.Errorf("tlsmint: read ca: %w", err)
	}
	caCert, err := decodeCert(caPEM)
	if err != nil {
		return nil, false, fmt.Errorf("tlsmint: parse ca: %w", err)
	}
	tlsCert, err := tls.LoadX509KeyPair(leafCertPath, leafKeyPath)
	if err != nil {
		return nil, false, fmt.Errorf("tlsmint: load leaf pair: %w", err)
	}
	leafCert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return nil, false, fmt.Errorf("tlsmint: parse leaf: %w", err)
	}
	return &Bundle{
		Dir:            dir,
		CACert:         caCert,
		LeafCert:       leafCert,
		TLSCertificate: tlsCert,
	}, true, nil
}

func mint(dir string, opts Options) (*Bundle, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	if opts.CAValidFor <= 0 {
		opts.CAValidFor = 10 * 365 * 24 * time.Hour
	}
	if opts.LeafValidFor <= 0 {
		opts.LeafValidFor = 365 * 24 * time.Hour
	}

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tlsmint: gen ca key: %w", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          mustSerial(),
		Subject:               pkix.Name{CommonName: "TokenOps Local CA", Organization: []string{"TokenOps"}},
		NotBefore:             now().Add(-time.Minute),
		NotAfter:              now().Add(opts.CAValidFor),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
		MaxPathLenZero:        false,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("tlsmint: create ca: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, fmt.Errorf("tlsmint: parse minted ca: %w", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tlsmint: gen leaf key: %w", err)
	}
	dns, ips := mergeSANs(opts.Hostnames, opts.IPAddresses)
	leafTmpl := &x509.Certificate{
		SerialNumber:          mustSerial(),
		Subject:               pkix.Name{CommonName: "tokenops-proxy", Organization: []string{"TokenOps"}},
		NotBefore:             now().Add(-time.Minute),
		NotAfter:              now().Add(opts.LeafValidFor),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("tlsmint: create leaf: %w", err)
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, fmt.Errorf("tlsmint: parse minted leaf: %w", err)
	}

	if err := writePEM(filepath.Join(dir, CAKeyFile), "EC PRIVATE KEY", marshalECKey(caKey), 0o600); err != nil {
		return nil, err
	}
	if err := writePEM(filepath.Join(dir, CACertFile), "CERTIFICATE", caDER, 0o644); err != nil {
		return nil, err
	}
	if err := writePEM(filepath.Join(dir, LeafKeyFile), "EC PRIVATE KEY", marshalECKey(leafKey), 0o600); err != nil {
		return nil, err
	}
	if err := writePEM(filepath.Join(dir, LeafCert), "CERTIFICATE", leafDER, 0o644); err != nil {
		return nil, err
	}

	tlsCert := tls.Certificate{
		Certificate: [][]byte{leafDER, caDER},
		PrivateKey:  leafKey,
		Leaf:        leafCert,
	}
	return &Bundle{
		Dir:            dir,
		CACert:         caCert,
		LeafCert:       leafCert,
		TLSCertificate: tlsCert,
	}, nil
}

// CAPool returns an x509.CertPool containing the bundle's CA so test
// clients (or downstream verifiers) can trust the leaf without touching
// the OS trust store.
func (b *Bundle) CAPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(b.CACert)
	return pool
}

// TLSConfig returns a server tls.Config that presents the leaf certificate
// and pins the minimum TLS version to 1.2.
func (b *Bundle) TLSConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{b.TLSCertificate},
		MinVersion:   tls.VersionTLS12,
	}
}

func mergeSANs(hosts []string, ips []net.IP) ([]string, []net.IP) {
	seen := map[string]bool{"localhost": true}
	out := []string{"localhost"}
	for _, h := range hosts {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	addrs := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		dup := false
		for _, existing := range addrs {
			if existing.Equal(ip) {
				dup = true
				break
			}
		}
		if !dup {
			addrs = append(addrs, ip)
		}
	}
	return out, addrs
}

func mustSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic("tlsmint: rand serial: " + err.Error())
	}
	return n
}

func marshalECKey(k *ecdsa.PrivateKey) []byte {
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		panic("tlsmint: marshal ec key: " + err.Error())
	}
	return der
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("tlsmint: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		return fmt.Errorf("tlsmint: encode %s: %w", path, err)
	}
	return nil
}

func decodeCert(b []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("tlsmint: no PEM block")
	}
	return x509.ParseCertificate(block.Bytes)
}

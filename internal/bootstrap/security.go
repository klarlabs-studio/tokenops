package bootstrap

import (
	"go.klarlabs.de/tokenops/internal/contexts/security/dashauth"
	"go.klarlabs.de/tokenops/internal/contexts/security/tlsmint"
)

// TLSBundle is the local certificate bundle the daemon serves over, minted
// into certDir on first use for hostnames.
func TLSBundle(certDir string, hostnames []string) (*tlsmint.Bundle, error) {
	return tlsmint.EnsureBundle(certDir, tlsmint.Options{Hostnames: hostnames})
}

// APIAuthenticator checks the bearer token the local API requires.
func APIAuthenticator(token string) (*dashauth.Authenticator, error) {
	return dashauth.New(dashauth.Config{AdminToken: token})
}

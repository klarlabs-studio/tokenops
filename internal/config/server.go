package config

import (
	"time"
)

// DashboardConfig gates /dashboard + /api/* behind a shared-secret
// token. AdminToken empty → daemon mints + persists one to
// ~/.tokenops/dashboard.token on first start. Setting it explicitly
// (env-substituted via the loader) lets ops roll the secret without
// touching disk state.
type DashboardConfig struct {
	AdminToken string `yaml:"admin_token"`
}

// MDNSConfig gates the Bonjour/mDNS advertisement that makes the dashboard
// reachable at http://tokenops.local:<port>.
//
// It had no config at all: the daemon advertised unconditionally, on every
// interface, under an instance name built from the machine's hostname. A
// local-first tool broadcasting the operator's computer name to every
// network they join should at least be something they can turn off.
type MDNSConfig struct {
	// Enabled forces advertising on or off. Nil — the default — advertises
	// only when the daemon binds an address the LAN can actually reach,
	// because a record pointing at 127.0.0.1 tells a peer nothing while
	// still broadcasting the name.
	Enabled *bool `yaml:"enabled,omitempty"`
	// InstanceName replaces the hostname in the advertised service name,
	// for operators who want tokenops.local without publishing what their
	// laptop is called. Empty uses the hostname, as before.
	InstanceName string `yaml:"instance_name,omitempty"`
}

// ResilienceConfig wraps each provider proxy route with
// fortify's CircuitBreakerStream. Off by default; opt in to gain
// per-stream FirstByte / Idle / Total deadlines and per-provider
// circuit breakers across SSE streams. Zero-valued deadlines disable
// the corresponding watchdog (at least one must be positive when
// enabled).
type ResilienceConfig struct {
	Enabled          bool          `yaml:"enabled"`
	FirstByteTimeout time.Duration `yaml:"first_byte_timeout"`
	IdleTimeout      time.Duration `yaml:"idle_timeout"`
	TotalTimeout     time.Duration `yaml:"total_timeout"`
	// FailureThreshold is the consecutive-failure count that trips
	// the breaker for a given route. Defaults to 5 when zero.
	FailureThreshold uint32 `yaml:"failure_threshold"`
}

// TLSConfig configures TLS termination on the local proxy.
type TLSConfig struct {
	// Enabled toggles HTTPS. When false, the proxy serves plain HTTP.
	Enabled bool `yaml:"enabled"`
	// CertDir is the directory the auto-minted CA + leaf bundle lives in.
	// On first run TokenOps creates the bundle here; on subsequent runs
	// the same files are reused. Default ~/.tokenops/certs.
	CertDir string `yaml:"cert_dir"`
	// Hostnames are extra DNS SANs added to the leaf cert. Loopback names
	// (localhost, 127.0.0.1, ::1) are always included.
	Hostnames []string `yaml:"hostnames"`
}

// LogConfig configures the structured logger.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
}

// ShutdownConfig configures graceful shutdown behaviour.
type ShutdownConfig struct {
	Timeout time.Duration `yaml:"timeout"`
}

package config

import (
	"fmt"
	"net"
	"strings"
)

// ListenIsLoopback reports whether the daemon binds only this machine:
// a loopback IP or "localhost". A wildcard (":7878", "0.0.0.0", "::"), a
// LAN IP or any other name exposes the listener to the network.
func (c Config) ListenIsLoopback() bool {
	host := c.Listen
	if h, _, err := net.SplitHostPort(c.Listen); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ListenWarnings describes what the listen address exposes, for the boot
// log. Binding beyond loopback is a supported choice — LAN access, a
// container port mapping — so it is not a validation error; but on plain
// HTTP it puts an unauthenticated LLM relay on the network, with vendor
// keys crossing it in clear text, and mDNS then advertises it. The
// operator should hear that once, loudly, at startup.
func (c Config) ListenWarnings() []string {
	if c.ListenIsLoopback() || c.TLS.Enabled {
		return nil
	}
	return []string{fmt.Sprintf(
		"listen %s is reachable from the network without TLS: the provider routes "+
			"accept requests from any peer that can reach it, and vendor API keys cross "+
			"the network in clear text; bind 127.0.0.1 or set tls.enabled: true",
		c.Listen)}
}

// validateAllowedHosts accepts bare host names or IPs, with an optional
// port. A URL, a path or a wildcard would silently never match a Host
// header, which reads as "configured" while admitting nothing.
func validateAllowedHosts(hosts []string) error {
	for i, h := range hosts {
		if h == "" || strings.ContainsAny(h, "/*@ \t") {
			return fmt.Errorf("allowed_hosts[%d] %q must be a host name or IP, optionally with a port", i, h)
		}
	}
	return nil
}

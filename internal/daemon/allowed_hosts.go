package daemon

import (
	"os"
	"strings"

	"go.klarlabs.de/tokenops/internal/config"
)

// proxyAllowedHosts lists the Host names the proxy answers to beyond
// loopback and its listen address: every name the daemon itself tells
// clients to use, plus what the operator configured.
//
//   - tls.hostnames: the certificate is minted for them, so clients use them.
//   - tokenops.local, when mDNS advertises it: the URL hint then points
//     the CLI and MCP server at that name.
//   - the machine's own name (bare and .local), when the bind reaches the
//     LAN: that is how a peer addresses the box.
//   - allowed_hosts: anything else, such as a reverse proxy's name.
//
// hostname is os.Hostname(), passed in so the policy is testable.
func proxyAllowedHosts(cfg config.Config, hostname string) []string {
	hosts := append([]string{}, cfg.TLS.Hostnames...)
	hosts = append(hosts, cfg.AllowedHosts...)
	if mdnsDecision(cfg.MDNS, cfg.Listen).Advertise {
		hosts = append(hosts, "tokenops.local")
	}
	if !cfg.ListenIsLoopback() {
		if name := strings.TrimSuffix(strings.ToLower(hostname), ".local"); name != "" {
			hosts = append(hosts, name, name+".local")
		}
	}
	return hosts
}

// hostnameOrEmpty is os.Hostname, with an error read as "no name".
func hostnameOrEmpty() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

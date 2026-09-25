package daemon

import (
	"fmt"
	"net"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/version"
)

// mdnsPlan is the decision about advertising, separated from performing it
// so the policy is testable without a multicast interface.
type mdnsPlan struct {
	Advertise bool
	// InstanceName overrides the hostname-derived name; empty keeps it.
	InstanceName string
	// Reason explains a skip, for the boot log.
	Reason string
}

// mdnsDecision decides whether to advertise the daemon over mDNS.
//
// The default used to be "always", which on the default configuration meant
// broadcasting the machine's hostname to every network the operator joined
// in order to publish a record pointing at 127.0.0.1 — an address no peer
// can reach. The name went out; nothing usable came back.
//
// So the default is now tied to what the advertisement is for: announce the
// daemon when the LAN can actually reach it, and stay quiet when it cannot.
// That keeps tokenops.local working exactly where it was ever useful. An
// operator who wants it either way says so.
func mdnsDecision(cfg config.MDNSConfig, addr string) mdnsPlan {
	plan := mdnsPlan{InstanceName: cfg.InstanceName}
	if cfg.Enabled != nil {
		plan.Advertise = *cfg.Enabled
		if !plan.Advertise {
			plan.Reason = "mdns.enabled is false"
		}
		return plan
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		plan.Reason = fmt.Sprintf("could not parse listen address %q", addr)
		return plan
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		plan.Reason = "listening on loopback only, which no peer can reach; " +
			"set mdns.enabled: true to advertise anyway"
		return plan
	}
	plan.Advertise = true
	return plan
}

// mdnsTXT is the service's TXT record.
//
// The version was hardcoded to v0.10.0 and had been broadcast unchanged for
// forty-eight releases — a stale fact published on the network is worse than
// no fact, because a browser reading it has no way to know.
func mdnsTXT() []string {
	return []string{
		"path=/api/",
		"version=" + version.Version,
	}
}

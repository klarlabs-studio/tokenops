package daemon

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/grandcat/zeroconf"
)

// mDNS lets the daemon advertise itself as `tokenops.local` on the
// local network so operators (and the MCP tokenops_dashboard tool)
// can type a memorable hostname instead of 127.0.0.1:7878. The whole
// path is best-effort: if multicast is blocked or the platform's
// resolver doesn't honour _http._tcp records, we log a warning and
// keep the loopback URL as the canonical link. The dashboard tool
// surfaces whichever URL the hint file carries.

// startMDNSAdvertise registers a zeroconf service announcing the
// daemon's HTTP listener under the hostname "tokenops.local". Returns
// a closer the caller must invoke on shutdown and the public URL
// (e.g. "http://tokenops.local:7878") the advertise resolves to.
// Returns an empty URL when the bind addr's port can't be parsed or
// the zeroconf registration fails — the caller treats both as
// "no advertise, fall back to loopback".
func startMDNSAdvertise(addr string, tls bool) (close func(), publicURL string, err error) {
	_, portStr, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		return func() {}, "", fmt.Errorf("split host:port: %w", splitErr)
	}
	port, parseErr := strconv.Atoi(portStr)
	if parseErr != nil {
		return func() {}, "", fmt.Errorf("parse port: %w", parseErr)
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "tokenops"
	}
	// Instance name shows up in browsers like "TokenOps (laptop-name)"
	// — useful when several daemons run on the same LAN. Service type
	// _http._tcp is the standard advertise for browser-clickable HTTP
	// services; macOS Safari + Chrome both treat .local hostnames as
	// first-class URLs.
	//
	// RegisterProxy (not Register) lets us advertise the chosen
	// hostname "tokenops" instead of the OS hostname — the operator
	// types http://tokenops.local:port and mDNS resolves to whatever
	// IPs the daemon is reachable on. localIPs() collects every
	// non-loopback IPv4 on the host; an empty result lets the lib
	// fall back to the OS hostname's resolved IPs.
	instance := "TokenOps (" + sanitizeInstance(host) + ")"
	// Match the advertised IPs to the bind: if the daemon listens on
	// 127.0.0.1 only, advertise just the loopback so tokenops.local
	// resolves to a reachable address on the same host. When the
	// daemon binds a wildcard or LAN address, expose every non-loopback
	// interface so peers on the same LAN can reach it too.
	bindHost, _, _ := net.SplitHostPort(addr)
	var ips []string
	switch bindHost {
	case "127.0.0.1", "::1":
		ips = []string{"127.0.0.1"}
	default:
		ips = localIPs()
		if len(ips) == 0 {
			ips = []string{"127.0.0.1"}
		}
	}
	srv, regErr := zeroconf.RegisterProxy(
		instance,
		"_http._tcp",
		"local.",
		port,
		"tokenops",
		ips,
		[]string{
			"path=/dashboard",
			"version=v0.10.0",
		},
		nil, // advertise on all interfaces
	)
	if regErr != nil {
		return func() {}, "", regErr
	}
	scheme := "http"
	if tls {
		scheme = "https"
	}
	return func() { srv.Shutdown() }, fmt.Sprintf("%s://tokenops.local:%d", scheme, port), nil
}

// sanitizeInstance trims the OS hostname into a Bonjour-safe instance
// label — strips the trailing .local that some macOS configs carry
// and collapses whitespace so the broadcast name reads cleanly in
// service browsers.
func sanitizeInstance(h string) string {
	h = strings.TrimSuffix(h, ".local")
	h = strings.TrimSpace(h)
	if h == "" {
		return "tokenops"
	}
	return h
}

// localIPs collects every non-loopback, non-link-local IPv4 + IPv6
// address bound to the host. Used for the mDNS A/AAAA records so
// `tokenops.local` resolves to the daemon's reachable interfaces.
// Returning an empty slice is harmless — zeroconf falls back to the
// OS hostname's resolved IPs. Link-local addresses are skipped
// because Bonjour browsers treat them as transient.
func localIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]string, 0, 4)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}

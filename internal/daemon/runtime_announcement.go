package daemon

import (
	"log/slog"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/proxy"
)

// publishRuntimeAnnouncement advertises the dashboard endpoint and publishes
// the daemon URL hint used by the CLI and MCP server. It returns the paired
// cleanup so the runtime surface owns its complete lifecycle.
func publishRuntimeAnnouncement(cfg config.Config, server *proxy.Server, dashboardToken string, logger *slog.Logger) func() {
	mdnsClose := func() {}
	var (
		mdnsPublicURL  string
		mdnsAdvertised bool
	)
	mdnsPlan := mdnsDecision(cfg.MDNS, server.Addr())
	switch {
	case !mdnsPlan.Advertise:
		logger.Info("mdns advertise skipped", "reason", mdnsPlan.Reason)
	default:
		if closer, publicURL, err := startMDNSAdvertise(server.Addr(), server.TLSEnabled(), mdnsPlan.InstanceName); err != nil {
			logger.Info("mdns advertise unavailable; using loopback URL", "err", err)
		} else {
			mdnsClose = closer
			mdnsPublicURL = publicURL
			mdnsAdvertised = true
			logger.Info("mdns advertise live", "url", publicURL)
		}
	}

	// Failure to publish the hint is non-fatal: MCP can still tell the
	// operator to run `tokenops start` explicitly.
	hintPublished := false
	if hintPath, err := writeURLHint(server.Addr(), server.TLSEnabled(), mdnsPublicURL, dashboardToken); err != nil {
		logger.Warn("could not publish daemon URL hint", "err", err)
	} else {
		hintPublished = true
		logger.Info("daemon URL hint published", "path", hintPath, "mdns_advertised", mdnsAdvertised)
	}

	return func() {
		if hintPublished {
			if err := removeURLHint(); err != nil {
				logger.Warn("could not remove daemon URL hint", "err", err)
			}
		}
		mdnsClose()
	}
}

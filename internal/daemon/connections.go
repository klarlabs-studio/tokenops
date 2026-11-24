package daemon

import (
	"time"

	"go.klarlabs.de/tokenops/internal/capability/connections"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/sourceprobe"
	"go.klarlabs.de/tokenops/internal/proxy"
)

// connectionsFn computes each glance's connections, on the glance's own
// cadence: the turns it read, the readers' health, the tools on this
// machine (machine, sourceprobe.ThisMachine in the daemon), and the
// sources cfg runs. The dismissals are read from the
// config file each time, so "Don't show" from the menu bar holds at once,
// with no restart.
func connectionsFn(cfg config.Config, machine func() sourceprobe.Machine) proxy.ConnectionsFunc {
	path := configPath(cfg)
	return func(g headroom.Glance, health []state.SourceReport, now time.Time) []connections.Connection {
		live := cfg
		if path != "" {
			if c, err := config.ReadMutable(path); err == nil {
				live.VendorUsage.Dismissed = c.VendorUsage.Dismissed
			}
		}
		return connections.Compute(connections.Inputs{
			Config:    &live,
			Turns:     g.Turns,
			Health:    health,
			Reported:  g.Headroom.VendorRead(),
			Installed: sourceprobe.InstalledTools(machine()),
			Now:       now,
		})
	}
}

// configPath is the file the daemon was started with, or the default.
func configPath(cfg config.Config) string {
	if cfg.SourcePath != "" {
		return cfg.SourcePath
	}
	p, _ := config.DefaultPath()
	return p
}

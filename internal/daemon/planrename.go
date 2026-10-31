package daemon

import (
	"log/slog"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
)

// renameRetiredPlans writes the current name of every plan a vendor
// renamed into the config file and the plan history, so the files say
// what the daemon already reads them as. Every config read already maps a
// retired name to its plan; this makes the files agree, and nobody has to
// run anything after Anthropic renamed Max 20x to Ultra.
func renameRetiredPlans(cfg config.Config, logger *slog.Logger) {
	if cfg.SourcePath != "" {
		renamed, err := config.RenameRetiredPlans(cfg.SourcePath)
		if err != nil {
			logger.Warn("could not write the renamed plans into the config", "path", cfg.SourcePath, "err", err)
		}
		for _, r := range renamed {
			logger.Info("plan renamed in the config", "provider", r.Provider, "from", r.From, "to", r.To)
		}
	}
	f, err := planhistory.Default()
	if err != nil {
		return
	}
	n, err := f.RenameRetiredPlans()
	if err != nil {
		logger.Warn("could not write the renamed plans into the plan history", "path", f.Path, "err", err)
	}
	if n > 0 {
		logger.Info("plans renamed in the plan history", "changes", n)
	}
}

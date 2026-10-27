package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"go.klarlabs.de/tokenops/internal/infra/menubarapp"
)

// startMenubarRefresh refreshes the installed menu bar app to the one shipped
// beside this binary. It runs as a supervised task, off the start-up path,
// and never fails the daemon: a failure is logged and the daemon carries on.
func (s *startup) startMenubarRefresh() {
	if runtime.GOOS != "darwin" {
		return
	}
	s.sup.Go("menubar-refresh", func(context.Context) error {
		refreshMenubarOnStart(s.logger)
		return nil
	})
}

// refreshMenubarOnStart finds the shipped app beside the running binary and
// the installed copy in ~/Applications, and refreshes the second from the
// first.
func refreshMenubarOnStart(logger *slog.Logger) {
	exe, err := os.Executable()
	if err != nil {
		logger.Warn("menu bar refresh skipped: cannot resolve own executable", "err", err)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Warn("menu bar refresh skipped: no home directory", "err", err)
		return
	}
	dst := filepath.Join(home, "Applications", menubarapp.AppName)
	refreshMenubarAt(menubarapp.ShippedBeside(exe), dst, menubarapp.System(), logger)
}

// refreshMenubarAt runs the refresh and logs its outcome. Its result is for
// the caller that wants to know what happened.
func refreshMenubarAt(src, dst string, ops menubarapp.Ops, logger *slog.Logger) menubarapp.Result {
	res, err := menubarapp.Refresh(src, dst, ops)
	switch {
	case err != nil:
		logger.Warn("menu bar app refresh failed; the daemon is unaffected", "app", dst, "err", err)
	case res.Refreshed:
		logger.Info("menu bar app refreshed to this version", "app", dst, "relaunched", res.Relaunched)
	}
	return res
}

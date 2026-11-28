package main

import (
	"context"
	"net/http"
)

// appVersion is this app's version, set by the release build
// (scripts/build-menubar-app.sh, -X main.appVersion); "dev" from a checkout.
var appVersion = "dev"

// versionInfo is what the footer's version line is made from: the running
// daemon's /version and this app's own.
type versionInfo struct {
	Daemon string `json:"daemon,omitempty"`
	App    string `json:"app,omitempty"`
	// Updating is an install that replaced the daemon's binary, which the
	// daemon has not switched to yet; Installed is its version when the
	// daemon can tell it, and RestartsItself a daemon whose supervisor
	// switches on its own.
	Updating       bool   `json:"updating,omitempty"`
	Installed      string `json:"installed,omitempty"`
	RestartsItself bool   `json:"restarts_itself,omitempty"`
}

// line is the footer's one quiet line: the running daemon's version, the
// app's when it differs, and while an upgrade waits, what TokenOps is
// updating to rather than the number about to go stale. Empty when the
// daemon's version is not known.
func (v versionInfo) line() string {
	if v.Daemon == "" {
		return ""
	}
	head, current := "TokenOps "+v.Daemon, v.Daemon
	switch {
	case v.Updating && !v.RestartsItself && v.Installed != "":
		head += " · " + v.Installed + " installed; restart the daemon to use it"
	case v.Updating && v.Installed != "":
		head, current = "TokenOps updating to "+v.Installed, v.Installed
	case v.Updating:
		return "TokenOps updating…"
	}
	if v.App != "" && v.App != current && (!v.Updating || v.App != v.Installed) {
		head += " · menu bar " + v.App
	}
	return head
}

// versionOf reads the running daemon's version. A daemon that does not
// answer leaves it unknown: the footer then says nothing rather than
// guess.
func (m *menubar) versionOf(ctx context.Context) versionInfo {
	var out struct {
		Version        string `json:"version"`
		Updating       bool   `json:"updating"`
		Installed      string `json:"installed"`
		RestartsItself bool   `json:"restarts_itself"`
	}
	v := versionInfo{App: appVersion}
	if err := m.daemon.do(ctx, http.MethodGet, "/version", nil, &out); err == nil {
		v.Daemon, v.Updating, v.Installed, v.RestartsItself = out.Version, out.Updating, out.Installed, out.RestartsItself
	}
	return v
}

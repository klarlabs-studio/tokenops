// Package copilot reads GitHub Copilot's local OAuth token and polls
// the (internal but widely-used since 2022) `api.github.com/copilot_internal/user`
// endpoint to retrieve the operator's live quota snapshots. Same
// pattern every Copilot IDE plugin (copilot.vim, copilot.lua, JetBrains
// integration) uses — internal, undocumented, but stable.
//
// What we get back: `quota_snapshots.{chat, premium_interactions}` each
// with `percent_remaining`, `remaining`, `entitlement`, `overage_count`,
// `unlimited`, plus a `timestamp_utc` for freshness. This is the live
// signal that powers the IDE's status-bar Copilot indicator.
package copilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// appsEntry is one record in ~/.config/github-copilot/apps.json (or
// the legacy hosts.json). Each is a separate signed-in GitHub account;
// in practice operators have one, but we accept many and use the
// first non-empty token.
type appsEntry struct {
	User        string `json:"user"`
	OAuthToken  string `json:"oauth_token"`
	GitHubAppID string `json:"githubAppId"`
}

// ErrNoToken is returned when no Copilot OAuth token is available on
// disk. Callers (poller) treat this as "Copilot not signed in; stay
// idle, log at debug, retry on next tick" rather than a fatal error.
var ErrNoToken = errors.New("copilot: no oauth token found in apps.json / hosts.json")

// DefaultTokenPaths returns the candidate locations Copilot writes
// its credentials to. Newer versions of the CLI / IDE plugins use
// apps.json; older ones used hosts.json. We try both, in order.
func DefaultTokenPaths() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	return []string{
		filepath.Join(home, ".config", "github-copilot", "apps.json"),
		filepath.Join(home, ".config", "github-copilot", "hosts.json"),
		filepath.Join(home, ".copilot", "apps.json"),
		filepath.Join(home, ".copilot", "hosts.json"),
	}, nil
}

// LoadToken reads the first non-empty OAuth token from any of paths
// (in order). Returns ErrNoToken when no file exists or every file
// lacks a token — callers use errors.Is to disambiguate.
func LoadToken(paths []string) (string, error) {
	var lastErr error
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			lastErr = fmt.Errorf("read %s: %w", p, err)
			continue
		}
		var apps map[string]appsEntry
		if err := json.Unmarshal(data, &apps); err != nil {
			lastErr = fmt.Errorf("parse %s: %w", p, err)
			continue
		}
		for _, e := range apps {
			if e.OAuthToken != "" {
				return e.OAuthToken, nil
			}
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", ErrNoToken
}

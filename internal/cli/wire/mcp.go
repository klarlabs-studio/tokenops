// Package wire registers tokenops with the AI clients already installed on
// the machine, so setup is one command rather than a checklist.
//
// Everything here edits files that belong to somebody else — an MCP host's
// own configuration — so the rules are strict: parse before writing, refuse
// anything unparseable, keep a backup, write atomically, and touch only the
// tokenops entry.
package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ServerName is the key tokenops registers itself under in an MCP host's
// server map.
const ServerName = "tokenops"

// Result reports what EnsureMCPServer did, so init can tell the operator
// what actually changed rather than claiming success generically.
type Result struct {
	// Path is the host config that was inspected.
	Path string
	// Changed is false when the entry was already correct — re-running
	// init must be a genuine no-op.
	Changed bool
	// Created is true when tokenops was not registered at all before.
	Created bool
	// PreviousCommand is the command the entry pointed at before, when
	// it was repointed. Empty on a fresh registration.
	PreviousCommand string
}

// EnsureMCPServer registers `exe serve` as the tokenops MCP server in the
// host config at path, creating the file if needed.
//
// The command is always pinned to an absolute executable path. A bare
// "tokenops" resolves through PATH at spawn time, which is how a host can
// end up running a stale binary while the daemon runs a current one — the
// two disagree silently, and tools added in the newer build simply never
// appear.
func EnsureMCPServer(path, exe string) (Result, error) {
	if path == "" {
		return Result{}, errors.New("wire: empty host config path")
	}
	if exe == "" {
		return Result{}, errors.New("wire: empty executable path")
	}
	res := Result{Path: path}

	root, existed, err := loadJSON(path)
	if err != nil {
		return Result{}, err
	}

	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	existing, hadEntry := servers[ServerName].(map[string]any)
	if !hadEntry {
		res.Created = true
	} else if prev, ok := existing["command"].(string); ok {
		res.PreviousCommand = prev
	}

	// Preserve any fields the host or operator added (env, disabled
	// flags, transport hints) and correct only what we own.
	entry := map[string]any{}
	for k, v := range existing {
		entry[k] = v
	}
	entry["type"] = "stdio"
	entry["command"] = exe
	entry["args"] = []any{"serve"}
	if _, ok := entry["env"]; !ok {
		entry["env"] = map[string]any{}
	}

	if hadEntry && equalJSON(existing, entry) {
		return res, nil
	}

	servers[ServerName] = entry
	root["mcpServers"] = servers
	res.Changed = true

	if err := writeJSON(path, root, existed); err != nil {
		return Result{}, err
	}
	return res, nil
}

// loadJSON reads a host config. A missing file is an empty object; a
// malformed one is a hard error, because overwriting it would destroy
// every other MCP server the operator had configured.
func loadJSON(path string) (map[string]any, bool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-owned config path
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, false, nil
		}
		return nil, false, fmt.Errorf("wire: read %s: %w", path, err)
	}
	if len(b) == 0 {
		return map[string]any{}, true, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false, fmt.Errorf(
			"wire: %s is not valid JSON (%w) — refusing to overwrite it; fix or move the file and re-run", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, true, nil
}

// writeJSON backs up an existing file then replaces it atomically.
func writeJSON(path string, root map[string]any, existed bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("wire: create dir for %s: %w", path, err)
	}
	if existed {
		if prior, err := os.ReadFile(path); err == nil { //nolint:gosec // operator-owned config path
			if err := os.WriteFile(path+".bak", prior, 0o600); err != nil {
				return fmt.Errorf("wire: write backup for %s: %w", path, err)
			}
		}
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("wire: encode %s: %w", path, err)
	}
	b = append(b, '\n')
	tmp := path + ".tokenops.tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("wire: write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("wire: replace %s: %w", path, err)
	}
	return nil
}

func equalJSON(a, b map[string]any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

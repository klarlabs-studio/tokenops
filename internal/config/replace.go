package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/infra/filelock"
)

// WriteFile replaces the config at path with data, whole or not at all,
// at the config's permissions. It is for a config written from nothing
// (`tokenops init`); a change to an existing config goes through
// ReadMutable and WriteMutable, which keep what the change does not touch.
func WriteFile(path string, data []byte) error {
	return replaceFile(path, func([]byte, bool) ([]byte, error) { return data, nil })
}

// replaceFile replaces the file at path with what next makes of its
// current content. A nil result leaves the file as it is.
//
// The CLI, the MCP server and the daemon all change this file, so the
// read, the change and the write happen under one lock: a second writer
// waits and then works from what the first wrote. The new content goes to
// a file beside the config and is renamed over it, so a reader, or a
// process killed half way, finds the old config or the new one and never
// part of either.
func replaceFile(path string, next func(current []byte, exists bool) ([]byte, error)) error {
	// A config kept as a symlink (a dotfiles repository) stays one: the
	// file it points at is the one replaced.
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	unlock, err := lockBeside(target)
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer unlock()

	current, err := os.ReadFile(target) //nolint:gosec // the operator's own config
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	data, err := next(current, exists)
	if err != nil || data == nil {
		return err
	}
	if err := writeAtomic(target, data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// lockBeside takes the lock that every writer of the file at path takes,
// and returns the function that releases it. The lock is a file of its
// own: the config is replaced by a rename, and a lock on the file renamed
// away would hold nobody.
func lockBeside(path string) (func(), error) {
	name := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".lock")
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, ConfigFilePerm) //nolint:gosec // beside the operator's own config
	if err != nil {
		return nil, err
	}
	unlock, err := filelock.Lock(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		unlock()
		_ = f.Close()
	}, nil
}

// writeAtomic replaces path with data at the config's permissions.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	// Synced before the rename: a rename that survives a power cut must
	// not name a file whose content did not.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), ConfigFilePerm); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	// The rename itself is durable once the directory is synced. Not every
	// platform can sync a directory, and the config is already whole.
	if d, err := os.Open(dir); err == nil { //nolint:gosec // the config's own directory
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// SPDX-License-Identifier: AGPL-3.0-only

// Package desktop composes the desktop build over the open core of internal/app, as internal/app/site composes the site
// (specs/2026-11-07-desktop-app.md DA1, DA16.4): the data directory of the author, the PostgreSQL cluster in it, the
// control channel with the shell, the order in which the processes live and stop, and the build's modules. cmd/desktop
// runs it.
package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// DataDir is the data directory of the author (DA1, «Data»): everything the app keeps lives under it, private to the
// user of the computer.
type DataDir struct {
	Root string
}

var errDataDir = errors.New("desktop: the data directory must be an absolute path")

// DefaultDataDir is where the shell keeps the data of the user (DA1, «Data»): %LOCALAPPDATA%\CanonKeeper on Windows,
// ~/Library/Application Support/CanonKeeper on macOS, $XDG_DATA_HOME/canonkeeper (~/.local/share/canonkeeper) on Linux.
// The shell names it in `start`; a backend started by hand falls back to it.
func DefaultDataDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "CanonKeeper"), nil
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "CanonKeeper"), nil
		}
	default:
		if data := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(data) {
			return filepath.Join(data, "canonkeeper"), nil
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "share", "canonkeeper"), nil
		}
	}
	return "", errDataDir
}

func (data DataDir) Cluster() string        { return filepath.Join(data.Root, "pg") }
func (data DataDir) Socket() string         { return filepath.Join(data.Root, "pg.run") }
func (data DataDir) Secret() string         { return filepath.Join(data.Root, "pg.secret") }
func (data DataDir) CodexHome() string      { return filepath.Join(data.Root, "codex", "home") }
func (data DataDir) CodexSQLite() string    { return filepath.Join(data.Root, "codex", "sqlite") }
func (data DataDir) CodexPrivate() string   { return filepath.Join(data.Root, "codex", "private") }
func (data DataDir) CodexTemp() string      { return filepath.Join(data.Root, "codex", "tmp") }
func (data DataDir) CodexWorkspace() string { return filepath.Join(data.Root, "codex", "workspace") }
func (data DataDir) Files() string          { return filepath.Join(data.Root, "files") }
func (data DataDir) Backups() string        { return filepath.Join(data.Root, "backups") }
func (data DataDir) Logs() string           { return filepath.Join(data.Root, "logs") }

// Prepare makes the directories with no access for other users; a directory that exists keeps its files and gets the
// same mode.
func (data DataDir) Prepare() error {
	if !filepath.IsAbs(data.Root) {
		return errDataDir
	}
	for _, directory := range []string{
		data.Root, data.Socket(), data.CodexHome(), data.CodexSQLite(), data.CodexPrivate(), data.CodexTemp(),
		data.CodexWorkspace(), data.Files(), data.Backups(), data.Logs(),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return err
		}
	}
	return nil
}

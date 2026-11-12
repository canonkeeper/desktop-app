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
	"strings"
)

// DataDir is the data directory of the author (DA1, «Data»): everything the app keeps lives under it, private to the
// user of the computer.
type DataDir struct {
	Root string
}

var (
	errDataDir     = errors.New("desktop: the data directory must be an absolute path")
	errNetworkPath = errors.New("desktop: a network path of Windows is not a directory of the app")
)

// appIdentifier is the identifier of the app the shell is built with; on Windows the data of the author live in its
// directory of %LOCALAPPDATA%, which the uninstaller removes only when the author asks it to, and not in the directory
// the installer puts the program in (specs/2026-11-12-desk-process.md PR2.1).
const appIdentifier = "ru.canonkeeper.desktop"

// DefaultDataDir is where the shell keeps the data of the user (DA1, «Data»; PR2.1):
// %LOCALAPPDATA%\ru.canonkeeper.desktop\data on Windows, ~/Library/Application Support/CanonKeeper on macOS,
// $XDG_DATA_HOME/canonkeeper (~/.local/share/canonkeeper) on Linux. The shell names it in `start`; a backend started by
// hand falls back to it.
func DefaultDataDir() (string, error) {
	return defaultDataDir(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func defaultDataDir(system string, getenv func(string) string, userHome func() (string, error)) (string, error) {
	switch system {
	case "windows":
		if local := getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, appIdentifier, "data"), nil
		}
	case "darwin":
		if home, err := userHome(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "CanonKeeper"), nil
		}
	default:
		if data := getenv("XDG_DATA_HOME"); filepath.IsAbs(data) {
			return filepath.Join(data, "canonkeeper"), nil
		}
		if home, err := userHome(); err == nil {
			return filepath.Join(home, ".local", "share", "canonkeeper"), nil
		}
	}
	return "", errDataDir
}

// plainPath takes the prefix \\?\ off a path of Windows (PR2.2): the resource directory of Tauri comes with it, and the
// programs of PostgreSQL do not find themselves by such a path (DA1.9, finding 2). A network path \\?\UNC\… is refused.
func plainPath(path string) (string, error) {
	rest, extended := strings.CutPrefix(path, `\\?\`)
	if !extended {
		return path, nil
	}
	if len(rest) >= 4 && strings.EqualFold(rest[:4], `UNC\`) {
		return "", errNetworkPath
	}
	return rest, nil
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
func (data DataDir) Settings() string       { return filepath.Join(data.Root, "settings.json") }

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

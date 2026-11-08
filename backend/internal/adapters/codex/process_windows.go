// SPDX-License-Identifier: AGPL-3.0-only

package codex

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// startInOwnGroup starts the app-server in its own process group, so the console signals of the backend do not reach it.
func startInOwnGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// processGroup is the process of the app-server: Windows signals no group.
func processGroup(command *exec.Cmd) (int, error) { return command.Process.Pid, nil }

// terminateProcessGroup leaves the stop to the closed stdin: the app-server ends when its stdin ends
// (specs/2026-11-07-desktop-app.md S0: 0.01–0.16 s); killProcessGroup ends the process.
func terminateProcessGroup(*exec.Cmd, int) {}

func killProcessGroup(command *exec.Cmd, _ int) { _ = command.Process.Kill() }

// processEnvironment is the whole environment of the app-server on Windows (specs/2026-11-07-desktop-app.md S0): its
// homes, the profile folders and the temporary directory inside its private home, the system folders it needs to run
// and no proxy for loopback. Everything else of the backend stays out.
func processEnvironment(config transportConfig) []string {
	system := os.Getenv("SystemRoot")
	if system == "" {
		system = `C:\Windows`
	}
	return []string{
		"CODEX_HOME=" + config.CodexHome,
		"CODEX_SQLITE_HOME=" + config.CodexSQLiteHome,
		"USERPROFILE=" + config.PrivateHome,
		"HOME=" + config.PrivateHome,
		"APPDATA=" + filepath.Join(config.PrivateHome, "AppData", "Roaming"),
		"LOCALAPPDATA=" + filepath.Join(config.PrivateHome, "AppData", "Local"),
		"TEMP=" + config.TempDir,
		"TMP=" + config.TempDir,
		"SystemRoot=" + system,
		"windir=" + system,
		"ComSpec=" + filepath.Join(system, "System32", "cmd.exe"),
		"PATH=" + filepath.Join(system, "System32") + ";" + system,
		"PATHEXT=.COM;.EXE;.BAT;.CMD",
		"NO_PROXY=127.0.0.1,localhost,::1",
	}
}

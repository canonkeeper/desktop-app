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

// windowsProfileOverride is the profile of a thread on Windows (specs/2026-11-18-desk-windows-chat.md WC1.1). The sandbox
// of Windows without the rights of an administrator runs no thread whose profile reads only a part of the disk (Codex
// 0.167.2, codex-rs/sandboxing/src/windows.rs: no read of the root is a refusal to start the thread), so the filesystem
// table of canonkeeper-runtime gets the read of the root beside the read of the workspace — Codex merges the tables of its
// layers — and nothing that writes; its network stays off. A thread of the chat runs no program and reads no file: its
// tools are the saga tools of the backend. A key of an override holds no ":", so the value is a table.
const windowsProfileOverride = `permissions.canonkeeper-runtime.filesystem={":root"="read"}`

// platformOverrides are the -c of Windows after those of the build: the profile of a thread reads the whole disk.
func platformOverrides() []string { return []string{windowsProfileOverride} }

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

//go:build unix

// SPDX-License-Identifier: AGPL-3.0-only

package codex

import (
	"errors"
	"os/exec"
	"syscall"
)

var errProcessGroup = errors.New("codex: the app-server leads no process group of its own")

// startInOwnGroup starts the app-server as the leader of its own process group, so a signal reaches what it starts
// too; on Linux the kernel also kills it when the backend dies (parentDeathSignal).
func startInOwnGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	parentDeathSignal(command.SysProcAttr)
}

// processGroup is the group the started app-server leads.
func processGroup(command *exec.Cmd) (int, error) {
	pgid, err := syscall.Getpgid(command.Process.Pid)
	if err != nil || pgid != command.Process.Pid {
		return 0, errProcessGroup
	}
	return pgid, nil
}

// terminateProcessGroup asks the group to stop; killProcessGroup stops it.
func terminateProcessGroup(_ *exec.Cmd, group int) { _ = syscall.Kill(-group, syscall.SIGTERM) }

func killProcessGroup(_ *exec.Cmd, group int) { _ = syscall.Kill(-group, syscall.SIGKILL) }

// processEnvironment is the whole environment of the app-server: its homes and temporary directory, a fixed PATH and
// locale, no proxy for loopback. Everything else of the backend stays out.
func processEnvironment(config transportConfig) []string {
	return []string{
		"CODEX_HOME=" + config.CodexHome,
		"CODEX_SQLITE_HOME=" + config.CodexSQLiteHome,
		"HOME=" + config.PrivateHome,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"NO_PROXY=127.0.0.1,localhost,::1",
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"TMPDIR=" + config.TempDir,
		"TZ=UTC",
		"no_proxy=127.0.0.1,localhost,::1",
	}
}

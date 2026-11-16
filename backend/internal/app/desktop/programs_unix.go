// SPDX-License-Identifier: AGPL-3.0-only

//go:build unix

package desktop

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// startBound starts a program of PostgreSQL bound to the backend (specs/2026-11-16-desk-restore-orphans.md RO1.1), as
// the app-server of Codex is: it leads its own process group, so the cancel of its context ends it with what it started —
// initdb with the postgres of its steps — and a signal to the group of the backend does not reach it; on Linux the
// kernel kills it when the backend dies, however it dies (parentDeathSignal). The server pg_ctl start lets go of
// (startsServer) leaves the group and the signal by itself — pg_ctl starts it in a session of its own — and outlives both:
// postmaster.pid names it to stopLeftover.
//
// The kernel sends the signal of the death of the parent when the thread that started the program ends, not only the
// backend (go.dev/issue/27505), and Go ends a thread only with a goroutine that returns locked to it: no goroutine of the
// backend may return with runtime.LockOSThread not undone, or a program of PostgreSQL dies amid its work
// (TestNoGoroutineOfTheBackendEndsLockedToItsThread).
func startBound(command *exec.Cmd, _ bool) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	parentDeathSignal(command.SysProcAttr)
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return command.Start()
}

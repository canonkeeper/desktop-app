//go:build unix && !linux

// SPDX-License-Identifier: AGPL-3.0-only

package codex

import "syscall"

// parentDeathSignal has no kernel counterpart here: the app-server stops when the closed pipes of a dead backend end
// its stdin.
func parentDeathSignal(*syscall.SysProcAttr) {}

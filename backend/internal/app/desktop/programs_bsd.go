// SPDX-License-Identifier: AGPL-3.0-only

//go:build unix && !linux

package desktop

import "syscall"

// parentDeathSignal has no kernel counterpart here: what a program of a dead backend still writes, the undo of the next
// start waits out (removeForGood).
func parentDeathSignal(*syscall.SysProcAttr) {}

// SPDX-License-Identifier: AGPL-3.0-only

package codex

import "syscall"

// parentDeathSignal makes the kernel kill the app-server when the backend dies.
func parentDeathSignal(attributes *syscall.SysProcAttr) { attributes.Pdeathsig = syscall.SIGKILL }

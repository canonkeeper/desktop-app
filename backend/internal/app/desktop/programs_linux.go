// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import "syscall"

// parentDeathSignal makes the kernel kill a program of PostgreSQL when the backend dies.
func parentDeathSignal(attributes *syscall.SysProcAttr) { attributes.Pdeathsig = syscall.SIGKILL }

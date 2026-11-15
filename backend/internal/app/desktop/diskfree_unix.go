// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package desktop

import "syscall"

// diskFree is the place a user may still take on the file system of path (specs/2026-11-13-desk-upgrade.md UG4.6).
func diskFree(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package desktop

import (
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// diskFree is the place a user may still take on the volume of path (specs/2026-11-13-desk-upgrade.md UG4.6).
func diskFree(path string) (uint64, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	if ok, _, err := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&available)), 0, 0); ok == 0 {
		return 0, err
	}
	return available, nil
}

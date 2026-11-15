// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package bridge

import (
	"os"
	"syscall"
)

// openShared opens a file to read it, letting others read, write, delete and replace it meanwhile: the backend replaces and
// deletes mcp.json while a bridge reads it.
func openShared(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

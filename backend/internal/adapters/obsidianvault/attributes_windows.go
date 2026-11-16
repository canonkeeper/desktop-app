// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package obsidianvault

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// readFlags open a note for reading; Windows has no FIFO in a folder.
const readFlags = os.O_RDONLY

// attributes are the attributes of a file Windows keeps: hidden, system, a point of a reparse, a file only in the cloud.
func attributes(info fs.FileInfo) uint32 {
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && data != nil {
		return data.FileAttributes
	}
	return 0
}

// entryInfo says whether the look needs the information of an entry: on Windows always — its attributes say whether it
// is hidden, a system entry or a file only in the cloud.
func entryInfo(fs.FileMode, bool) bool { return true }

// errorSharingViolation is ERROR_SHARING_VIOLATION: another program has the file open without sharing it.
const errorSharingViolation syscall.Errno = 32

// heldByAnother says a failure is a hold of another program — Obsidian, a scanner, a synchronizer — that lets the file go in
// a moment (golang.org/issue/31247).
func heldByAnother(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, errorSharingViolation)
}

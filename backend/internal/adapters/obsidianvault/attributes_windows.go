// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package obsidianvault

import (
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

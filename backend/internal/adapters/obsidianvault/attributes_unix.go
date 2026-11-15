// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package obsidianvault

import (
	"io/fs"
	"os"
	"syscall"
)

// readFlags open a note without waiting: a FIFO put in the place of a note between the look and the read does not hold the
// read (OB0.2).
const readFlags = os.O_RDONLY | syscall.O_NONBLOCK

// attributes are none outside Windows: a name on a dot is what hides a file.
func attributes(fs.FileInfo) uint32 { return 0 }

// entryInfo says whether the look needs the information of an entry: its size, for a note.
func entryInfo(mode fs.FileMode, noteName bool) bool { return mode.IsRegular() && noteName }

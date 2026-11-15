// SPDX-License-Identifier: AGPL-3.0-only

package obsidianvault

import (
	"io/fs"

	"canonkeeper/backend/internal/domain/obsidian"
)

// The attributes of a file on Windows the bridge reads (fileapi.h); elsewhere a file has none.
const (
	attributeHidden             = 0x2
	attributeSystem             = 0x4
	attributeDirectory          = 0x10
	attributeReparsePoint       = 0x400
	attributeOffline            = 0x1000
	attributeRecallOnOpen       = 0x40000
	attributeRecallOnDataAccess = 0x400000
)

// foundOf is an entry by the type its directory gives — its links not followed — and its attributes (OB0.2). A symbolic
// link is a link. On Windows Go gives a junction, as any point of a reparse that names another place, no type of a
// folder, and a file or a folder of a cloud folder (OneDrive and the like: a point of a reparse that is not a link) the
// type «irregular»: a folder of a cloud keeps its type of a folder, a file of it is a file, and whether its data are on
// the disk say its attributes — a file only in the cloud is not read. A junction, a FIFO, a device and a socket are links.
func foundOf(name string, mode fs.FileMode, attributes uint32, size int64) obsidian.Found {
	found := obsidian.Found{Name: name, Size: size, Hidden: attributes&(attributeHidden|attributeSystem) != 0}
	cloudOnly := attributes&(attributeOffline|attributeRecallOnOpen|attributeRecallOnDataAccess) != 0
	switch {
	case mode&fs.ModeSymlink != 0:
		found.Link = true
	case mode.IsDir():
		found.Dir = true
	case mode.IsRegular():
		found.Regular, found.CloudOnly = true, cloudOnly
	case mode&fs.ModeIrregular != 0 && attributes&attributeReparsePoint != 0 && attributes&attributeDirectory == 0 &&
		mode&(fs.ModeNamedPipe|fs.ModeDevice|fs.ModeCharDevice|fs.ModeSocket) == 0:
		found.Regular, found.CloudOnly = true, cloudOnly
	default:
		found.Link = true
	}
	return found
}

// note says an entry is a note the bridge reads: a file on the disk, no link.
func note(found obsidian.Found) bool {
	return found.Regular && !found.Link && !found.CloudOnly
}

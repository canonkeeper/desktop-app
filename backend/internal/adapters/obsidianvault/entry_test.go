// SPDX-License-Identifier: AGPL-3.0-only

package obsidianvault

import (
	"io/fs"
	"testing"

	"canonkeeper/backend/internal/domain/obsidian"
)

// specs/2026-11-13-desk-obsidian.md OB0.2: an entry is taken by its own type and attributes. A link, a junction (a point of
// a reparse that names a folder: Go gives it no type of a folder), a FIFO, a device and a socket are links; a folder and a
// file of a cloud folder of Windows are read, a file of it only in the cloud is not; hidden and system entries are hidden.
func TestAnEntryIsTakenByItsOwnType(t *testing.T) {
	for name, test := range map[string]struct {
		mode       fs.FileMode
		attributes uint32
		want       obsidian.Found
	}{
		"file":          {mode: 0o644, want: obsidian.Found{Regular: true}},
		"folder":        {mode: fs.ModeDir | 0o755, want: obsidian.Found{Dir: true}},
		"symlink":       {mode: fs.ModeSymlink, want: obsidian.Found{Link: true}},
		"fifo":          {mode: fs.ModeNamedPipe, want: obsidian.Found{Link: true}},
		"device":        {mode: fs.ModeDevice | fs.ModeCharDevice, want: obsidian.Found{Link: true}},
		"socket":        {mode: fs.ModeSocket, want: obsidian.Found{Link: true}},
		"unknown":       {mode: fs.ModeIrregular, want: obsidian.Found{Link: true}},
		"junction":      {mode: fs.ModeIrregular, attributes: attributeReparsePoint | attributeDirectory, want: obsidian.Found{Link: true}},
		"cloud folder":  {mode: fs.ModeDir | fs.ModeIrregular, attributes: attributeReparsePoint | attributeDirectory, want: obsidian.Found{Dir: true}},
		"cloud file":    {mode: fs.ModeIrregular, attributes: attributeReparsePoint, want: obsidian.Found{Regular: true}},
		"cloud only":    {mode: fs.ModeIrregular, attributes: attributeReparsePoint | attributeRecallOnDataAccess, want: obsidian.Found{Regular: true, CloudOnly: true}},
		"recall open":   {mode: fs.ModeIrregular, attributes: attributeReparsePoint | attributeRecallOnOpen, want: obsidian.Found{Regular: true, CloudOnly: true}},
		"offline":       {mode: 0o644, attributes: attributeOffline, want: obsidian.Found{Regular: true, CloudOnly: true}},
		"hidden file":   {mode: 0o644, attributes: attributeHidden, want: obsidian.Found{Regular: true, Hidden: true}},
		"system folder": {mode: fs.ModeDir, attributes: attributeSystem | attributeDirectory, want: obsidian.Found{Dir: true, Hidden: true}},
	} {
		got := foundOf("x.md", test.mode, test.attributes, 0)
		test.want.Name = "x.md"
		if got != test.want {
			t.Fatalf("%s: %+v, want %+v", name, got, test.want)
		}
	}
}

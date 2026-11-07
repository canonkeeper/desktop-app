// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"golang.org/x/sys/windows"
)

// localAppData asks the shell for the folder of the process token, not the environment: a
// process started for another user (Start-Process -Credential) inherits the caller's variables.
func localAppData() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
}

// shortPath is the 8.3 alias of an existing path (empty when the volume has no 8.3 names).
func shortPath(path string) (string, error) {
	long, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, 1024)
	n, err := windows.GetShortPathName(long, &buffer[0], uint32(len(buffer)))
	if err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:n]), nil
}

func activeCodePage() uint32 {
	return windows.GetACP()
}

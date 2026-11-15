// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package bridge

import "os"

// openShared opens a file to read it.
func openShared(path string) (*os.File, error) { return os.Open(path) }

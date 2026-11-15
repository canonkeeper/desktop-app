// SPDX-License-Identifier: AGPL-3.0-only

//go:build smokeupdate

package desktop

import "strings"

// smokeMark is the mark of the smoke build of an update (specs/2026-11-13-desk-updates.md UB3.7), the same the shell of
// that build carries: the release check refuses a program with it (UB3.3). Only the job of the smoke run builds with the
// tag smokeupdate (scripts/desktop-backend.mjs --smoke).
var smokeMark = "CANONKEEPER_SMOKE_BUILD_OF_UPDATES"

func init() {
	smokeInstall = strings.HasPrefix(smokeMark, "CANONKEEPER_SMOKE")
}

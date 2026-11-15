// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import "time"

// The times of the daily check of a new version (specs/2026-11-13-desk-updates.md UB1.2): the first half a minute after
// ready, then once a day from the last check; the schedule looks every minute, and the wait for the turns of the chat
// before an installation every two seconds (UB2.1). The process tests build the program with shorter ones (go build
// -overlay); no setting and no environment changes them.
var (
	updateFirstCheck  = 30 * time.Second
	updateDay         = 24 * time.Hour
	updateLook        = time.Minute
	updateTurnsLook   = 2 * time.Second
	updateTurnsWithin = 10 * time.Second
)

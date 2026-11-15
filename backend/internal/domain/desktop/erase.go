// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"errors"
	"strings"
)

// EraseWord is the word the author writes to erase all the data of the app on the computer
// (specs/2026-11-13-desk-erase.md ER1.1, ER1.2): the page compares it before it asks, the server again before it asks the
// shell, so a request without the word erases nothing.
const EraseWord = "стереть"

// ErrEraseUnconfirmed is a request to erase without the word.
var ErrEraseUnconfirmed = errors.New("desktop: the word of the erasure is not written")

// EraseConfirmed says whether the author wrote the word: whatever its case and the spaces at its ends.
func EraseConfirmed(word string) bool {
	return strings.EqualFold(strings.TrimSpace(word), EraseWord)
}

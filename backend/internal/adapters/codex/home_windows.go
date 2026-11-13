// SPDX-License-Identifier: AGPL-3.0-only

package codex

import (
	"errors"
	"syscall"
)

// errorSharingViolation is ERROR_SHARING_VIOLATION, which syscall does not name.
const errorSharingViolation syscall.Errno = 32

// heldByAnother says a rename of the home failed because another process — a scanner reading what the copy has just
// written — holds a file of it for a moment: ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION, the errors the go command
// waits out (cmd/internal/robustio). ERROR_FILE_NOT_FOUND is not a hold: a home without agents is not waited for.
func heldByAnother(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, errorSharingViolation)
}

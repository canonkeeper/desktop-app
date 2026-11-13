// SPDX-License-Identifier: MIT

package provider

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// newID names a translated response or output item.
func newID(prefix string) string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failure is unrecoverable in-process; never fall back to a
		// predictable ID because translated items would collide.
		panic(errors.New("provider: secure response ID generation failed: " + err.Error()))
	}
	return prefix + hex.EncodeToString(raw[:])
}

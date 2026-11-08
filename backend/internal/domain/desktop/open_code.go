// SPDX-License-Identifier: AGPL-3.0-only

// Package desktop holds the rules of the desktop build that need no process, network or database
// (specs/2026-11-07-desktop-app.md): the one-time codes that open the window of the local author.
package desktop

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// OpenCodeLifetime is how long a code opens the window (DA3.2).
const OpenCodeLifetime = 30 * time.Second

// ErrInvalidOpenCode is a code that is unknown, used or older than OpenCodeLifetime: they are not told apart.
var ErrInvalidOpenCode = errors.New("desktop: invalid open code")

// OpenCodes are the one-time codes of the window (DA3.2): 32 random bytes each, issued only into the control channel of
// the shell, good once and for OpenCodeLifetime, compared in constant time. Issuing a new code ends the previous one: the
// shell asks for a code when it opens a window, and only that window may use it.
type OpenCodes struct {
	mu      sync.Mutex
	now     func() time.Time
	code    []byte
	expires time.Time
}

// NewOpenCodes keeps codes against the clock now.
func NewOpenCodes(now func() time.Time) *OpenCodes {
	return &OpenCodes{now: now}
}

// Issue makes the next code, in the URL-safe base64 of its 32 bytes.
func (codes *OpenCodes) Issue() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	codes.mu.Lock()
	defer codes.mu.Unlock()
	codes.code, codes.expires = []byte(code), codes.now().Add(OpenCodeLifetime)
	return code
}

// Use spends the code: it opens the window once while it lives; any other code — and this one used or late — is
// ErrInvalidOpenCode. A wrong code does not spend the right one.
func (codes *OpenCodes) Use(code string) error {
	codes.mu.Lock()
	defer codes.mu.Unlock()
	if codes.code == nil || subtle.ConstantTimeCompare(codes.code, []byte(code)) != 1 {
		return ErrInvalidOpenCode
	}
	live := codes.now().Before(codes.expires)
	codes.code = nil
	if !live {
		return ErrInvalidOpenCode
	}
	return nil
}

// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

// specs/2026-11-07-desktop-app.md DA3.2: a code of 32 random bytes opens the window once and for 30 s; a new code ends
// the previous one; a wrong code spends nothing.
func TestAnOpenCodeWorksOnceAndForThirtySeconds(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	codes := NewOpenCodes(func() time.Time { return now })

	code := codes.Issue()
	if raw, err := base64.RawURLEncoding.DecodeString(code); err != nil || len(raw) != 32 {
		t.Fatalf("code %q decodes to %d bytes, %v", code, len(raw), err)
	}
	if err := codes.Use(code + "x"); !errors.Is(err, ErrInvalidOpenCode) {
		t.Fatalf("a wrong code = %v", err)
	}
	if err := codes.Use(code); err != nil {
		t.Fatalf("the code = %v", err)
	}
	if err := codes.Use(code); !errors.Is(err, ErrInvalidOpenCode) {
		t.Fatalf("the code used twice = %v", err)
	}

	late := codes.Issue()
	now = now.Add(OpenCodeLifetime)
	if err := codes.Use(late); !errors.Is(err, ErrInvalidOpenCode) {
		t.Fatalf("a code of 30 s = %v", err)
	}

	first := codes.Issue()
	second := codes.Issue()
	if first == second {
		t.Fatal("two codes are equal")
	}
	if err := codes.Use(first); !errors.Is(err, ErrInvalidOpenCode) {
		t.Fatalf("the previous code = %v", err)
	}
	if err := codes.Use(second); err != nil {
		t.Fatalf("the last code = %v", err)
	}
	if err := codes.Use(""); !errors.Is(err, ErrInvalidOpenCode) {
		t.Fatalf("no code = %v", err)
	}
}

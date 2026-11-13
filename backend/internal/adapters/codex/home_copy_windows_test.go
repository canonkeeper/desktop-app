// SPDX-License-Identifier: AGPL-3.0-only

package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// specs/2026-11-13-desk-home.md HM1 on Windows: a file of the home held open — a scanner reads it — makes the rename of
// its tree fail; the copy waits the hold out and puts the profile in place instead of stopping the start.
func TestTheCopyWaitsOutAFileOfTheHomeAScannerHolds(t *testing.T) {
	first := profile(t, filepath.Join(t.TempDir(), "first"), "first", false)
	home := filepath.Join(t.TempDir(), "home")
	if err := CopyHome(home, first); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(filepath.Join(home, "skills", "chat", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if err := os.Rename(filepath.Join(home, "skills"), filepath.Join(home, "skills-aside")); err == nil || !heldByAnother(err) {
		t.Fatalf("the rename of a tree with a held file = %v; want a hold", err)
	}
	release := time.AfterFunc(300*time.Millisecond, func() { _ = held.Close() })
	t.Cleanup(func() { release.Stop() })

	second := profile(t, filepath.Join(t.TempDir(), "second"), "second", false)
	if err := CopyHome(home, second); err != nil {
		t.Fatalf("the copy while a scanner holds a file of the home: %v", err)
	}
	theCopyOf(t, home, second)
}

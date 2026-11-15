// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// specs/2026-11-13-desk-account.md AC2.3, AC1.5: a pass the app dropped while the shell may keep its record is marked in
// settings.json by a salted SHA-256, never by the pass; the next start knows the record by the mark, another pass is not
// marked, and the mark goes once the record went. The keys the account does not own stay.
func TestADroppedPassIsMarkedWithoutThePass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"port": 24567, "shell": {"x": 1}}`)
	saved, err := loadSettings(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	const pass = "ckd_pass-of-the-test-north-00000000000000000000"
	if saved.Dropped(pass) {
		t.Fatal("a pass is dropped before any mark")
	}
	if err := saved.MarkDropped(pass); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte(pass)) || bytes.Contains(raw, []byte("pass-of-the-test")) || !bytes.Contains(raw, []byte(`"account"`)) ||
		!bytes.Contains(raw, []byte(`"shell"`)) {
		t.Fatalf("settings.json = %s", raw)
	}
	again, err := loadSettings(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !again.Dropped(pass) || again.Dropped(pass+"x") || again.Dropped("") {
		t.Fatal("the mark does not know its pass alone")
	}
	if err := again.ForgetDropped(); err != nil {
		t.Fatal(err)
	}
	if third, _ := loadSettings(path, time.Now()); third.Dropped(pass) {
		t.Fatal("the mark stayed")
	}
	if values := readSettingsFile(t, path); compact(t, values["shell"]) != `{"x":1}` || string(values["port"]) != "24567" {
		t.Fatalf("the other keys = %v", values)
	}
}

// specs/2026-11-13-desk-account-ai.md AI2.2, AI2.3: the salt of the refs of the permits is made once and read again at every
// start of the same data directory — a Job taken again after a restart asks its permit by the same ref —; the other keys of
// settings.json stay, and another data directory has another salt.
func TestTheSaltOfTheRefsOutlivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"port": 24567, "shell": {"x": 1}}`)
	saved, err := loadSettings(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	salt, err := saved.WorkSalt()
	if err != nil || len(salt) != 32 {
		t.Fatalf("the salt = %x %v", salt, err)
	}
	again, err := loadSettings(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if restarted, err := again.WorkSalt(); err != nil || !bytes.Equal(restarted, salt) {
		t.Fatalf("the salt after a restart = %x %v; want %x", restarted, err, salt)
	}
	if values := readSettingsFile(t, path); compact(t, values["shell"]) != `{"x":1}` || string(values["port"]) != "24567" {
		t.Fatalf("the other keys = %v", values)
	}
	other, err := loadSettings(filepath.Join(t.TempDir(), "settings.json"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if elsewhere, _ := other.WorkSalt(); bytes.Equal(elsewhere, salt) {
		t.Fatal("another data directory has the same salt")
	}
}

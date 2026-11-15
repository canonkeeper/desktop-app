// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
)

// settingsAccount is the section account of settings.json (specs/2026-11-13-desk-account.md AC2.3): the mark of a pass
// the app dropped — the site refused it, the author unlinked or the shell did not keep it — while the shell may still
// keep its record. The mark is a salted SHA-256 of the pass, never the pass; the next start removes the record it marks
// before anything and is not linked.
const settingsAccount = "account"

// accountFile is the section account.
type accountFile struct {
	Dropped *droppedFile `json:"dropped,omitempty"`
}

type droppedFile struct {
	Salt string `json:"salt"`
	Hash string `json:"sha256"`
}

func droppedHash(salt, pass string) string {
	sum := sha256.Sum256([]byte(salt + "\n" + pass))
	return hex.EncodeToString(sum[:])
}

// MarkDropped marks the record of pass for the next start.
func (current *settings) MarkDropped(pass string) error {
	salt := rand.Text()
	encoded, err := json.Marshal(accountFile{Dropped: &droppedFile{Salt: salt, Hash: droppedHash(salt, pass)}})
	if err != nil {
		return err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.write(settingsAccount, encoded)
}

// ForgetDropped takes the mark away: the record it marked is gone or holds a pass of a new link.
func (current *settings) ForgetDropped() error {
	current.mu.Lock()
	defer current.mu.Unlock()
	if _, ok := current.values[settingsAccount]; !ok {
		return nil
	}
	return current.writeAll(map[string]json.RawMessage{settingsAccount: nil})
}

// Dropped says whether pass is the pass the mark names.
func (current *settings) Dropped(pass string) bool {
	current.mu.Lock()
	defer current.mu.Unlock()
	var section accountFile
	if pass == "" || json.Unmarshal(current.values[settingsAccount], &section) != nil || section.Dropped == nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(droppedHash(section.Dropped.Salt, pass)), []byte(section.Dropped.Hash)) == 1
}

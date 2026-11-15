// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"time"
)

// settingsJournal is the section journal of settings.json: when the author cleared the journal of what went out last
// (specs/2026-11-14-desk-journal.md JR2.6).
const settingsJournal = "journal"

type journalSection struct {
	ClearedAt string `json:"clearedAt"`
}

// LogClearedAt is when the author cleared the journal last; nil before he did.
func (current *settings) LogClearedAt() *time.Time {
	current.mu.Lock()
	defer current.mu.Unlock()
	var section journalSection
	if json.Unmarshal(current.values[settingsJournal], &section) != nil {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, section.ClearedAt)
	if err != nil {
		return nil
	}
	return &at
}

// KeepLogCleared keeps when the author cleared the journal.
func (current *settings) KeepLogCleared(at time.Time) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	encoded, err := json.Marshal(journalSection{ClearedAt: at.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	return current.write(settingsJournal, encoded)
}

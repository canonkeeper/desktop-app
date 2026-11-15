// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"time"
)

// The section updates of settings.json (specs/2026-11-13-desk-updates.md UB1.2, UB2.8): daily — the check once a day, as
// «Добро пожаловать» and «Настройки» set it; checked — when the app last asked for a new version; notes — the notes of the
// release being installed with its version, until the start of that version names them once. The backend is the one
// writer; other keys of the section stay.
type updatesSection struct {
	Checked string        `json:"checked,omitempty"`
	Notes   *releaseNotes `json:"notes,omitempty"`
}

type releaseNotes struct {
	Version string `json:"version"`
	Text    string `json:"text"`
}

// updatesKeys reads the section updates as its keys, so that a write keeps the keys it does not know.
func (current *settings) updatesKeys() map[string]json.RawMessage {
	section := map[string]json.RawMessage{}
	_ = json.Unmarshal(current.values[settingsUpdates], &section)
	if section == nil {
		section = map[string]json.RawMessage{}
	}
	return section
}

func (current *settings) updates() updatesSection {
	var section updatesSection
	_ = json.Unmarshal(current.values[settingsUpdates], &section)
	return section
}

// writeUpdates replaces keys of the section; a nil value takes its key away.
func (current *settings) writeUpdates(changes map[string]any) error {
	section := current.updatesKeys()
	for key, value := range changes {
		if value == nil {
			delete(section, key)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		section[key] = encoded
	}
	encoded, err := json.Marshal(section)
	if err != nil {
		return err
	}
	return current.write(settingsUpdates, encoded)
}

// Daily says whether the app checks for a new version once a day.
func (current *settings) Daily() bool {
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.checkUpdates
}

// KeepDaily turns the daily check on or off (updateAppUpdateSettings).
func (current *settings) KeepDaily(daily bool) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	if err := current.writeUpdates(map[string]any{"daily": daily}); err != nil {
		return err
	}
	current.checkUpdates = daily
	return nil
}

// Checked is when the app last asked for a new version; zero when it never did.
func (current *settings) Checked() time.Time {
	current.mu.Lock()
	defer current.mu.Unlock()
	checked, err := time.Parse(time.RFC3339, current.updates().Checked)
	if err != nil {
		return time.Time{}
	}
	return checked
}

// keepChecked remembers when the app asked for a new version: the next daily check is a day later (UB1.2).
func (current *settings) keepChecked(at time.Time) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.writeUpdates(map[string]any{"checked": at.UTC().Format(time.RFC3339Nano)})
}

// keepNotes remembers the notes of the release being installed with its version (UB2.8).
func (current *settings) keepNotes(version, text string) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	if plainVersion(version) == "" {
		return nil
	}
	return current.writeUpdates(map[string]any{"notes": releaseNotes{Version: version, Text: text}})
}

// releaseNotesOf are the notes kept for version, when the running program is the version they were kept with.
func (current *settings) releaseNotesOf(version string) string {
	current.mu.Lock()
	defer current.mu.Unlock()
	notes := current.updates().Notes
	if notes == nil || version == "" || notes.Version != version {
		return ""
	}
	return notes.Text
}

// forgetNotes takes the notes away once the running version reached theirs: ready named them, or the version that runs
// is later. Notes of a later version than the running one stay — its installation did not happen yet.
func (current *settings) forgetNotes(running string) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	notes := current.updates().Notes
	if notes == nil || newerVersion(notes.Version, running) {
		return nil
	}
	return current.writeUpdates(map[string]any{"notes": nil})
}

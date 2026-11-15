// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"

	domain "canonkeeper/backend/internal/domain/desktop"
)

// settingsPrograms is the section programs of settings.json (specs/2026-11-15-desk-mcp.md MC3.1): whether the programs of the
// author may read the sagas, and whether the author ever let them. Only the backend writes it.
const settingsPrograms = "programs"

// Programs is the reading of the sagas by the programs of the author as the settings keep it; off without the section.
func (current *settings) Programs() domain.Programs {
	current.mu.Lock()
	defer current.mu.Unlock()
	var section domain.Programs
	if json.Unmarshal(current.values[settingsPrograms], &section) != nil {
		return domain.Programs{}
	}
	return section
}

// KeepPrograms keeps the reading of the sagas by the programs; turned on once, it was let ever after.
func (current *settings) KeepPrograms(programs domain.Programs) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	encoded, err := json.Marshal(domain.Programs{Enabled: programs.Enabled, Ever: programs.Ever || programs.Enabled})
	if err != nil {
		return err
	}
	return current.write(settingsPrograms, encoded)
}

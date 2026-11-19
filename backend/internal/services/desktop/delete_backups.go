// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import domain "canonkeeper/backend/internal/domain/desktop"

// BackupRemoval removes the copies before an update: every copy of backups/, whatever else lies there stays.
type BackupRemoval interface {
	Remove() error
}

// UpdateGate is the gate of new turns of the chat as the removal of the copies hears it: closed while an update is
// installed (specs/2026-11-13-desk-updates.md UB2.1).
type UpdateGate interface {
	Admit() (func(), error)
}

// DeleteBackups is «Удалить копии…» of «Настройки → Данные» (specs/2026-11-19-desk-trust.md TW3.4, TW3.5): the author who
// deleted a book removes its trace in the copies before an update, keeping his sagas. While an update is installed it is
// ErrUpdating and nothing is removed — the next start of a new version makes its own copy.
type DeleteBackups struct {
	Backups BackupRemoval
	Gate    UpdateGate
}

func (useCase DeleteBackups) Execute() error {
	release, err := useCase.Gate.Admit()
	if err != nil {
		return domain.ErrUpdating
	}
	release()
	return useCase.Backups.Remove()
}

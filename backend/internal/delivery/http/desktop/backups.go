// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"errors"
	"net/http"

	domain "canonkeeper/backend/internal/domain/desktop"
	"canonkeeper/backend/internal/httpapi"
)

// DeleteBackups removes the copies before an update (services/desktop.DeleteBackups).
type DeleteBackups interface {
	Execute() error
}

// deleteBackups is deleteDesktopBackups (specs/2026-11-19-desk-trust.md TW3.5): «Удалить копии…» of «Настройки → Данные»
// removes every copy of backups/ and answers 204; getDesktopState lists none after it. While an update is installed it is
// 409 desktop.updating and nothing is removed.
func (handlers Handlers) deleteBackups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	err := handlers.Backups.Execute()
	if errors.Is(err, domain.ErrUpdating) {
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "an update of the app is installed", "desktop.updating")
		return
	}
	if err != nil {
		unexpected(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

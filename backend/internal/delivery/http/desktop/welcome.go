// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"net/http"

	"canonkeeper/backend/internal/httpapi"
)

// Welcome marks «Добро пожаловать» as seen with the choice of the author (services/desktop.DismissWelcome).
type Welcome interface {
	Execute(ctx context.Context, checkUpdates bool) error
}

type welcomeJSON struct {
	CheckUpdates *bool `json:"checkUpdates"`
}

// dismissWelcome is dismissWelcome (specs/2026-11-13-desk-upgrade.md UG5.1): the author closed «Добро пожаловать» with
// the choice to check for a new version once a day; settings.json keeps both, and getDesktopState reads them. A body
// without the choice marks nothing.
func (handlers Handlers) dismissWelcome(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	var request welcomeJSON
	if _, ok := decode(r, &request); !ok || request.CheckUpdates == nil {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "invalid request body", "validation.failed")
		return
	}
	if err := handlers.Welcome.Execute(r.Context(), *request.CheckUpdates); err != nil {
		unexpected(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"net/http"

	domain "canonkeeper/backend/internal/domain/desktop"
	"canonkeeper/backend/internal/httpapi"
)

// Erase asks the shell to erase all the data of the app (services/desktop.EraseData).
type Erase interface {
	Execute(ctx context.Context, word string) error
}

type eraseJSON struct {
	Confirm string `json:"confirm"`
}

// eraseData is eraseDesktopData (specs/2026-11-13-desk-erase.md ER1.2): with the word «стереть» the shell is asked once
// and the answer is 202 — the shell got the request; another word is desktop.erase_unconfirmed and asks nothing.
func (handlers Handlers) eraseData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	var request eraseJSON
	if _, ok := decode(r, &request); !ok {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "invalid request body", "validation.failed")
		return
	}
	err := handlers.Erase.Execute(r.Context(), request.Confirm)
	if errors.Is(err, domain.ErrEraseUnconfirmed) {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "the word of the erasure is not written", "desktop.erase_unconfirmed")
		return
	}
	if err != nil {
		unexpected(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

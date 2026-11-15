// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"

	domain "canonkeeper/backend/internal/domain/desktop"
)

// EraseShell is the shell of the app as the erasure needs it: the one that erases. The backend erases nothing itself.
type EraseShell interface {
	AskErase() error
}

// AccountUnlink is the link of the app with an account as the erasure needs it: it tries to unlink the app on the site
// within its term, whatever the site answers (specs/2026-11-13-desk-account.md AC2.5).
type AccountUnlink interface {
	BeforeErase(ctx context.Context)
}

// EraseData is «Стереть все данные» of «Настройки → Данные» (specs/2026-11-13-desk-erase.md ER1.2): with the word of the
// author the backend tries to unlink the app from the account of the site, then asks its shell to erase all the data of
// the app on the computer — the shell stops the backend, closes the window, removes what the app wrote, the records of
// its secrets among them, and ends. Without the word nothing is asked.
type EraseData struct {
	Shell   EraseShell
	Account AccountUnlink
}

func (useCase EraseData) Execute(ctx context.Context, word string) error {
	if !domain.EraseConfirmed(word) {
		return domain.ErrEraseUnconfirmed
	}
	useCase.Account.BeforeErase(ctx)
	return useCase.Shell.AskErase()
}

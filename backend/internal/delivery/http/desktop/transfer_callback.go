// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"net/http"
)

// The words of the pages of the return of a transfer (specs/2026-11-16-desk-site-saga.md TR5.1): the browser is answered at
// once — the saga comes and opens in the app, minutes after — with the heading and the paragraph of the board
// (specs/2026-11-16-desk-boards13.md, Data «страница возврата», 44·81); a return that is not the one waiting sends the author
// back to the app to begin the transfer again.
const (
	transferGoesHeading = "Сага переносится в CanonKeeper"
	transferGoes        = "Вернитесь в приложение — окно браузера можно закрыть."
	transferRefused     = "Эта ссылка переноса уже не действует. Вернитесь в CanonKeeper и начните перенос снова"
)

// TransferCallback is the return of the page /transfer of the site, GET /desktop/transfer/callback?code&state (TR5.1): with
// the return of the link one of the two routes of the window without its session, behind the guard of the Host of the window.
// Only GET with one code and one state; a refusal is 403 with a page that says nothing of why. The state spends the waiting
// at once and the download goes on in a term of its own, never the request of the browser. The pages have no script and no
// style, are not kept by the browser and repeat nothing of the request; the code and the state go into no log.
type TransferCallback struct {
	Finish interface {
		Execute(code, state string) error
	}
}

func (callback TransferCallback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if r.Method != http.MethodGet || len(query) != 2 || len(query["code"]) != 1 || len(query["state"]) != 1 {
		writeCallback(w, http.StatusForbidden, transferRefused)
		return
	}
	// The one refusal of the return is a state that is not the one waiting (domain.ErrStateInvalid).
	if err := callback.Finish.Execute(query.Get("code"), query.Get("state")); err != nil {
		writeCallback(w, http.StatusForbidden, transferRefused)
		return
	}
	writePage(w, http.StatusOK, transferGoesHeading, transferGoes)
}

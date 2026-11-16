// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"html"
	"net/http"
	"time"

	domain "canonkeeper/backend/internal/domain/accountlink"
	service "canonkeeper/backend/internal/services/accountlink"
)

// The words of the pages of the return (specs/2026-11-13-desk-account.md AC1.2, Data): a return that did not link sends the
// author back to the app to link it again; the page names no store — where the link was not kept the app says by passStore —
// and no button — after a failure the app says «Связать снова» (specs/2026-11-15-desk-account-link-web.md AW1.6).
const (
	callbackLinked   = "Готово. Вернитесь в окно CanonKeeper — эту вкладку можно закрыть"
	callbackFailed   = "Связать не получилось. Вернитесь в CanonKeeper и свяжите приложение ещё раз"
	callbackNotSaved = "Связь не сохранилась на этом компьютере. Вернитесь в CanonKeeper и свяжите приложение ещё раз"
	callbackRefused  = "Эта ссылка связи уже не действует. Вернитесь в CanonKeeper и свяжите приложение ещё раз"
)

// CallbackTerm bounds the exchange and the keeping of the pass a return starts; the request of the browser does not
// bound them (AC1.3).
const CallbackTerm = 2 * time.Minute

// LinkCallback is the return of the page /link of the site, GET /desktop/link/callback?code&state (AC1.2): the one route
// of the app without the session of the window, behind the guard of the Host of the window. Only GET with one code and
// one state; a refusal is 403 with a page that says nothing of why. The exchange goes on in its own term when the
// browser leaves. The pages have no script and no style, are not kept by the browser and repeat nothing of the request;
// the code and the state go into no log.
type LinkCallback struct {
	Finish interface {
		Execute(ctx context.Context, code, state string) (service.Outcome, error)
	}
	Term time.Duration
}

func (callback LinkCallback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if r.Method != http.MethodGet || len(query) != 2 || len(query["code"]) != 1 || len(query["state"]) != 1 {
		writeCallback(w, http.StatusForbidden, callbackRefused)
		return
	}
	term := callback.Term
	if term == 0 {
		term = CallbackTerm
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), term)
	type finished struct {
		outcome service.Outcome
		err     error
	}
	done := make(chan finished, 1)
	go func() {
		defer cancel()
		outcome, err := callback.Finish.Execute(ctx, query.Get("code"), query.Get("state"))
		done <- finished{outcome: outcome, err: err}
	}()
	var result finished
	select {
	case result = <-done:
	case <-r.Context().Done():
		return
	}
	switch {
	case errors.Is(result.err, domain.ErrStateInvalid):
		writeCallback(w, http.StatusForbidden, callbackRefused)
	case result.err != nil:
		writeCallback(w, http.StatusInternalServerError, callbackFailed)
	case result.outcome == service.Linked:
		writeCallback(w, http.StatusOK, callbackLinked)
	case result.outcome == service.NotSaved:
		writeCallback(w, http.StatusServiceUnavailable, callbackNotSaved)
	default:
		writeCallback(w, http.StatusBadGateway, callbackFailed)
	}
}

// writeCallback is a page of the return: plain HTML of one paragraph under the name of the app.
func writeCallback(w http.ResponseWriter, status int, words string) {
	writePage(w, status, "CanonKeeper", words)
}

// writePage is a page of the return of its heading and one paragraph: plain HTML, not kept by the browser.
func writePage(w http.ResponseWriter, status int, heading, words string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<!doctype html><html lang="ru"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"><title>CanonKeeper</title></head>` +
		`<body><main><h1>` + html.EscapeString(heading) + `</h1><p>` + html.EscapeString(words) + `</p></main></body></html>`))
}

// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	domain "canonkeeper/backend/internal/domain/accountlink"
)

type transferFinishOfTheTest struct {
	err           error
	codes, states []string
}

func (finish *transferFinishOfTheTest) Execute(code, state string) error {
	finish.codes, finish.states = append(finish.codes, code), append(finish.states, state)
	return finish.err
}

// specs/2026-11-16-desk-site-saga.md TR5.1; specs/2026-11-16-desk-boards13.md, Data «страница возврата» (44·81): the browser
// that brought the code is answered at once with the words of the board — a heading and a paragraph of plain HTML, no script
// and no style, not kept —, and a return the app does not take with its own refusal; neither repeats the code or the state.
func TestTheReturnOfATransferSaysTheWordsOfTheBoard(t *testing.T) {
	const code, state = "code-of-the-test-north-00000000000000000000", "state-of-the-test-north-0000000000000000000"
	for _, tc := range []struct {
		name   string
		err    error
		status int
		words  []string
	}{
		{"a return the app takes", nil, http.StatusOK, []string{"<h1>Сага переносится в CanonKeeper</h1>", "<p>Вернитесь в приложение — окно браузера можно закрыть.</p>"}},
		{"a return the app does not take", domain.ErrStateInvalid, http.StatusForbidden, []string{"<p>Эта ссылка переноса уже не действует. Вернитесь в CanonKeeper и начните перенос снова</p>"}},
	} {
		finish := &transferFinishOfTheTest{err: tc.err}
		response := httptest.NewRecorder()
		TransferCallback{Finish: finish}.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/desktop/transfer/callback?code="+code+"&state="+state, nil))
		body := response.Body.String()
		if response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: %d %v", tc.name, response.Code, response.Header())
		}
		for _, words := range tc.words {
			if !strings.Contains(body, words) {
				t.Fatalf("%s: the page %s does not say %s", tc.name, body, words)
			}
		}
		if strings.Contains(body, "<script") || strings.Contains(body, "style") || strings.Contains(body, code) || strings.Contains(body, state) {
			t.Fatalf("%s: the page = %s", tc.name, body)
		}
		if len(finish.codes) != 1 || finish.codes[0] != code || finish.states[0] != state {
			t.Fatalf("%s: the return was finished with %v %v", tc.name, finish.codes, finish.states)
		}
	}
}

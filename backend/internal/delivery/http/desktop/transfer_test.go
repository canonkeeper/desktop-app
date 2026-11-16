// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/accountlink"
	service "canonkeeper/backend/internal/services/accountlink"
	"canonkeeper/backend/internal/services/sagaarchive"

	"github.com/go-chi/chi/v5"
)

// transferSessions are the sessions of the window of a test with the proof of their writes.
type transferSessions struct{ windowSessions }

func (sessions transferSessions) AuthenticateWrite(ctx context.Context, cookie, csrf string) (auth.User, auth.SessionWriteProof, error) {
	user, err := sessions.CurrentUser(ctx, cookie)
	if err != nil {
		return auth.User{}, auth.SessionWriteProof{}, err
	}
	if csrf != "token-of-the-window" {
		return auth.User{}, auth.SessionWriteProof{}, auth.ErrInvalidCSRF
	}
	return user, auth.SessionWriteProof{SessionHash: "session-of-the-window", CSRFSecretHash: "csrf-of-the-window"}, nil
}

// transferOfTheTest answers the transfer it is set to and records the starts.
type transferOfTheTest struct {
	transfer service.Transfer
	err      error
	starts   []string
	proofs   []auth.SessionWriteProof
}

func (fake *transferOfTheTest) Execute(_ context.Context, actor string, proof auth.SessionWriteProof) (service.Transfer, error) {
	fake.starts, fake.proofs = append(fake.starts, actor), append(fake.proofs, proof)
	return fake.transfer, fake.err
}

type readTransfer struct{ transfer service.Transfer }

func (read readTransfer) Execute() service.Transfer { return read.transfer }

type cancelTransfer struct {
	transfer service.Transfer
	err      error
	ids      *[]string
}

func (cancel cancelTransfer) Execute(id string) (service.Transfer, error) {
	if cancel.ids != nil {
		*cancel.ids = append(*cancel.ids, id)
	}
	return cancel.transfer, cancel.err
}

// cancelOf is the body of cancelSiteSagaTransfer that names the transfer of the test.
const cancelOf = `{"id":"00000000-0000-4000-8000-000000000201"}`

func transferWindow(start *transferOfTheTest, read service.Transfer, cancel cancelTransfer) http.Handler {
	router := chi.NewRouter()
	SagaTransfer{
		Sessions: transferSessions{windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}}},
		Start:    start, Read: readTransfer{transfer: read}, Cancel: cancel, AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)
	return router
}

func transferCall(router http.Handler, method string, change func(*http.Request)) (int, string) {
	var body io.Reader
	if method == http.MethodDelete {
		body = strings.NewReader(cancelOf)
	}
	request := httptest.NewRequest(method, "/desktop/saga-transfer", body)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Origin", "http://127.0.0.1:1")
	request.Header.Set("X-CSRF-Token", "token-of-the-window")
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "cookie-of-the-window"})
	if change != nil {
		change(request)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code, response.Body.String()
}

// specs/2026-11-16-desk-site-saga.md TR2.1, TR2.2: the operations of the transfer are the window's — its session, and for the
// writes its Origin and its CSRF token —; the start takes the proof of the write of the session; the refusals of the start
// and of the cancel have their codes; the answer says what the phase has and nothing else.
func TestTheWindowStartsReadsAndStopsATransfer(t *testing.T) {
	waiting := service.Transfer{
		ID: "00000000-0000-4000-8000-000000000201", Phase: domain.TransferWaiting,
		Address: "https://site.example.test/transfer?port=24567&request=r&state=s", Expires: time.Date(2026, 11, 16, 9, 40, 0, 0, time.UTC),
		Email: "a…@example.test",
	}
	start := &transferOfTheTest{transfer: waiting}
	router := transferWindow(start, waiting, cancelTransfer{transfer: service.Transfer{Phase: domain.TransferNone}})
	status, body := transferCall(router, http.MethodPost, nil)
	if status != http.StatusOK || body != `{"id":"00000000-0000-4000-8000-000000000201","phase":"waiting","address":"https://site.example.test/transfer?port=24567\u0026request=r\u0026state=s","expiresAt":"2026-11-16T09:40:00.000Z","email":"a…@example.test","name":null,"bytes":null,"code":null,"projectId":null,"opened":null}`+"\n" {
		t.Fatalf("startSiteSagaTransfer = %d %s", status, body)
	}
	if len(start.starts) != 1 || start.starts[0] != "author" || start.proofs[0].SessionHash != "session-of-the-window" {
		t.Fatalf("the start = %v %v", start.starts, start.proofs)
	}
	for _, refusal := range []struct {
		name   string
		change func(*http.Request)
		status int
		code   string
	}{
		{"no CSRF", func(r *http.Request) { r.Header.Del("X-CSRF-Token") }, http.StatusForbidden, "csrf.invalid"},
		{"another Origin", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:2") }, http.StatusForbidden, ""},
		{"no Origin", func(r *http.Request) { r.Header.Del("Origin") }, http.StatusForbidden, ""},
		{"no session", func(r *http.Request) { r.Header.Del("Cookie") }, http.StatusUnauthorized, "auth.session_required"},
	} {
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			if status, body := transferCall(router, method, refusal.change); status != refusal.status || !strings.Contains(body, refusal.code) {
				t.Fatalf("%s %s = %d %s", refusal.name, method, status, body)
			}
		}
	}
	if len(start.starts) != 1 {
		t.Fatal("a refused write started a transfer")
	}
	if status, body := transferCall(router, http.MethodGet, func(r *http.Request) { r.Header.Del("Cookie") }); status != http.StatusUnauthorized {
		t.Fatalf("getSiteSagaTransfer without the session = %d %s", status, body)
	}

	for _, refusal := range []struct {
		err  error
		code string
	}{
		{domain.ErrUpdating, "desktop.updating"}, {domain.ErrTransferRunning, "saga_transfer.running"}, {domain.ErrNotLinked, "account.not_linked"},
	} {
		router := transferWindow(&transferOfTheTest{err: refusal.err}, waiting, cancelTransfer{})
		if status, body := transferCall(router, http.MethodPost, nil); status != http.StatusConflict || !strings.Contains(body, `"code":"`+refusal.code+`"`) {
			t.Fatalf("%s = %d %s", refusal.code, status, body)
		}
	}
	opening := transferWindow(start, waiting, cancelTransfer{err: domain.ErrTransferOpening})
	if status, body := transferCall(opening, http.MethodDelete, nil); status != http.StatusConflict || !strings.Contains(body, `"code":"saga_transfer.opening"`) {
		t.Fatalf("a cancel while the saga opens = %d %s", status, body)
	}

	// TR2.2: the cancel names its transfer by its id; a body that is not {id} with an id cancels nothing.
	var ids []string
	named := transferWindow(start, waiting, cancelTransfer{transfer: service.Transfer{Phase: domain.TransferNone}, ids: &ids})
	if status, body := transferCall(named, http.MethodDelete, nil); status != http.StatusOK || !strings.Contains(body, `"phase":"none"`) {
		t.Fatalf("a cancel of its transfer = %d %s", status, body)
	}
	for _, refused := range []string{"", "{}", `{"id":null}`, `{"id":""}`, `{"id":"not-a-transfer"}`, `{"id":"00000000-0000-4000-8000-000000000201","more":1}`, `[]`} {
		status, body := transferCall(named, http.MethodDelete, func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(refused))
			r.ContentLength = int64(len(refused))
		})
		if status != http.StatusBadRequest || !strings.Contains(body, `"code":"validation.failed"`) {
			t.Fatalf("a cancel with %q = %d %s", refused, status, body)
		}
	}
	if len(ids) != 1 || ids[0] != "00000000-0000-4000-8000-000000000201" {
		t.Fatalf("the cancels named %v", ids)
	}

	for _, tc := range []struct {
		transfer service.Transfer
		want     string
	}{
		{service.Transfer{Phase: domain.TransferNone}, `{"id":null,"phase":"none","address":null,"expiresAt":null,"email":null,"name":null,"bytes":null,"code":null,"projectId":null,"opened":null}`},
		{service.Transfer{ID: "t", Phase: domain.TransferDownloading, Name: "Север.cksaga", Bytes: 7340032, Address: "x", Email: "y"},
			`{"id":"t","phase":"downloading","address":null,"expiresAt":null,"email":null,"name":"Север.cksaga","bytes":7340032,"code":null,"projectId":null,"opened":null}`},
		{service.Transfer{ID: "t", Phase: domain.TransferFailed, Code: domain.TransferCut},
			`{"id":"t","phase":"failed","address":null,"expiresAt":null,"email":null,"name":null,"bytes":null,"code":"download_cut","projectId":null,"opened":null}`},
		{service.Transfer{ID: "t", Phase: domain.TransferDone, Name: "Север.cksaga", ProjectID: "p", Opened: &sagaarchive.Opened{ProjectID: "p", Name: "Север"}},
			`{"id":"t","phase":"done","address":null,"expiresAt":null,"email":null,"name":"Север.cksaga","bytes":null,"code":null,"projectId":"p","opened":{"projectId":"p","name":"Север"`},
	} {
		router := transferWindow(start, tc.transfer, cancelTransfer{})
		if status, body := transferCall(router, http.MethodGet, nil); status != http.StatusOK || !strings.HasPrefix(body, tc.want) {
			t.Fatalf("getSiteSagaTransfer %s = %d %s", tc.transfer.Phase, status, body)
		}
	}
}

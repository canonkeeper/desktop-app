package authormemory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/authormemory"
	"canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/authormemory"

	"github.com/go-chi/chi/v5"
)

const (
	testProject = "90000000-0000-4000-8000-000000000001"
	testRecord  = "90000000-0000-4000-8000-000000000002"
)

var testProof = auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"}

type userStub struct{}

func (userStub) CurrentUser(_ context.Context, cookie string) (auth.User, error) {
	if cookie == "" {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return auth.User{ID: "user-1"}, nil
}

func (userStub) AuthenticateWrite(_ context.Context, cookie, csrf string) (auth.User, auth.SessionWriteProof, error) {
	if cookie == "" {
		return auth.User{}, auth.SessionWriteProof{}, auth.ErrUnauthenticated
	}
	if csrf != "token" {
		return auth.User{}, auth.SessionWriteProof{}, auth.ErrInvalidCSRF
	}
	return auth.User{ID: "user-1"}, testProof, nil
}

type storeStub struct {
	canWrite bool
	access   error
	filter   domain.Filter
	created  []service.CreateCommand
	updated  []domain.Draft
	expected []int64
	deleted  []string
	err      error
}

func (stub *storeStub) CanWriteAuthorMemory(context.Context, string, string) (bool, error) {
	return stub.canWrite, stub.access
}

func testRecordValue() domain.Record {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	return domain.Record{
		ID: testRecord, ProjectID: testProject, Kind: domain.KindPlan, Text: "Кира узнает правду", ChapterID: "chapter-1", Name: "Кира",
		Revision: 1, AuthorName: "Лена", CreatedAt: at, UpdatedAt: at,
	}
}

func (stub *storeStub) ListAuthorMemory(_ context.Context, _ service.Actor, filter domain.Filter) (domain.Page, error) {
	stub.filter = filter
	first := testRecordValue()
	first.Origin = domain.Origin{Kind: domain.OriginChat, ChatID: "chat-1", Title: "Что Мара знает"}
	gone := domain.Record{ID: "other", Kind: domain.KindRejected, Text: "нет", ChapterID: "gone", CreatedAt: time.Unix(1, 0), Origin: domain.Origin{Kind: domain.OriginManual}}
	return domain.Page{Records: []domain.Record{first, gone}, Total: 5, HasMore: true, Shared: true}, stub.err
}

func (stub *storeStub) CreateAuthorMemory(_ context.Context, command service.CreateCommand) (domain.Record, error) {
	stub.created = append(stub.created, command)
	return testRecordValue(), stub.err
}

func (stub *storeStub) UpdateAuthorMemory(_ context.Context, _ service.Write, _ string, expected int64, draft domain.Draft) (domain.Record, error) {
	stub.updated, stub.expected = append(stub.updated, draft), append(stub.expected, expected)
	return testRecordValue(), stub.err
}

func (stub *storeStub) DeleteAuthorMemory(_ context.Context, _ service.Write, id string, expected int64) error {
	stub.deleted, stub.expected = append(stub.deleted, id), append(stub.expected, expected)
	return stub.err
}

type chaptersStub struct{}

func (chaptersStub) ChapterVersion(context.Context, string, string) (*sagamemory.Version, error) {
	return sagamemory.NewVersion(testProject, "published", "fingerprint", []sagamemory.Chapter{
		{ID: "chapter-1", BookID: "book-1", BookOrder: 1, BookTitle: "Том", Order: 1, Title: "Глава 12. Порт"},
	}), nil
}

func newRouter(store *storeStub) http.Handler {
	router := chi.NewRouter()
	NewHandlers(UseCases{
		List:   service.List{Store: store, Chapters: chaptersStub{}},
		Create: service.Create{Access: store, Store: store, Chapters: chaptersStub{}, Now: time.Now},
		Update: service.Update{Access: store, Store: store, Chapters: chaptersStub{}},
		Delete: service.Delete{Access: store, Store: store},
	}, userStub{}, []string{"https://example.test"}).Mount(router)
	return router
}

type request struct {
	method, path, body string
	noCookie, noCSRF   bool
	key                string
	foreign            bool
}

func serve(router http.Handler, input request) *httptest.ResponseRecorder {
	r := httptest.NewRequest(input.method, input.path, strings.NewReader(input.body))
	if !input.noCookie {
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session"})
	}
	if input.method != http.MethodGet {
		r.Header.Set("Origin", "https://example.test")
		if input.foreign {
			r.Header.Set("Origin", "https://evil.test")
		}
		if !input.noCSRF {
			r.Header.Set("X-CSRF-Token", "token")
		}
	}
	if input.key != "" {
		r.Header.Set("Idempotency-Key", input.key)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func problemCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatalf("problem = %s: %v", w.Body.String(), err)
	}
	return problem.Code
}

const collection = "/projects/" + testProject + "/author-memory"

// specs/2026-11-01-writer-assistant-index.md, R4.5: a reader lists the records with who, when, the chapter
// by its key; a chapter that is gone is simply not named; the page says how many there are and goes on.
func TestListShowsTheRecordsOfTheProject(t *testing.T) {
	store := &storeStub{}
	w := serve(newRouter(store), request{method: http.MethodGet, path: collection + "?kind=plan&q=Кира&limit=2"})
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list = %d: %s", w.Code, w.Body.String())
	}
	if len(store.filter.Kinds) != 1 || store.filter.Kinds[0] != domain.KindPlan || store.filter.Query != "Кира" || store.filter.Limit != 2 {
		t.Fatalf("filter = %+v", store.filter)
	}
	var page struct {
		Data []struct {
			ID, Kind, Text, Name, Author string
			Revision                     int
			Chapter                      *struct{ Key, Label, Title, BookTitle string }
			ChapterGone                  bool `json:"chapterGone"`
			Origin                       struct{ Kind, ChatID, Title string }
		} `json:"data"`
		Shared bool `json:"shared"`
		Meta   struct {
			HasMore    bool    `json:"hasMore"`
			NextCursor *string `json:"nextCursor"`
			TotalCount int     `json:"totalCount"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	first := page.Data[0]
	if first.Kind != "plan" || first.Text != "Кира узнает правду" || first.Name != "Кира" || first.Author != "Лена" || first.Revision != 1 ||
		first.Chapter == nil || first.Chapter.Key != "12" || first.Chapter.BookTitle != "Том" {
		t.Fatalf("record = %+v", first)
	}
	// specs/2026-11-10-ux1-names.md N5.1: the chapter of a record is named for the author beside its key.
	if first.Chapter.Label != "Книга 1, гл. 12 „Порт“" {
		t.Fatalf("the place of the chapter = %q", first.Chapter.Label)
	}
	if page.Data[1].Chapter != nil || !page.Data[1].ChapterGone || first.ChapterGone {
		t.Fatalf("a chapter that is gone = %+v", page.Data[1])
	}
	// specs/2026-11-10-ux8-memory-records.md MR1.2, MR1.5: where each record came from, and whether the saga is shared.
	if first.Origin.Kind != "chat" || first.Origin.ChatID != "chat-1" || first.Origin.Title != "Что Мара знает" || page.Data[1].Origin.Kind != "manual" || !page.Shared {
		t.Fatalf("origins = %+v, %+v, shared %v", first.Origin, page.Data[1].Origin, page.Shared)
	}
	if !page.Meta.HasMore || page.Meta.NextCursor == nil || page.Meta.TotalCount != 5 {
		t.Fatalf("meta = %+v", page.Meta)
	}
	next := serve(newRouter(store), request{method: http.MethodGet, path: collection + "?cursor=" + *page.Meta.NextCursor})
	if next.Code != http.StatusOK || store.filter.Cursor == nil || store.filter.Cursor.ID != "other" {
		t.Fatalf("the next page = %d, %+v", next.Code, store.filter)
	}
	for _, query := range []string{"?kind=fact", "?limit=0", "?limit=101", "?cursor=@@", "?unknown=1", "?kind=plan&kind=rejected"} {
		if w := serve(newRouter(&storeStub{}), request{method: http.MethodGet, path: collection + query}); w.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", query, w.Code)
		}
	}
	if w := serve(newRouter(&storeStub{}), request{method: http.MethodGet, path: collection, noCookie: true}); w.Code != http.StatusUnauthorized {
		t.Fatalf("without a session = %d", w.Code)
	}
	if w := serve(newRouter(&storeStub{err: domain.ErrNotFound}), request{method: http.MethodGet, path: collection}); w.Code != http.StatusNotFound {
		t.Fatalf("an outsider = %d", w.Code)
	}
}

// A mutation needs the session, the CSRF token, the right origin; an add its Idempotency-Key; a change the
// revision it saw; and an editor or above. Who may not write learns nothing about the chapters.
func TestWritesAreFencedAsTheNeighbouringEndpoints(t *testing.T) {
	item := collection + "/" + testRecord
	body := `{"kind":"plan","text":" Кира узнает правду ","chapter":"12","name":"Кира"}`
	for name, test := range map[string]struct {
		input request
		store *storeStub
		code  int
		kind  string
	}{
		"no session":     {request{method: http.MethodPost, path: collection, body: body, key: "key-12345", noCookie: true}, &storeStub{canWrite: true}, 401, "auth.session_required"},
		"no csrf":        {request{method: http.MethodPost, path: collection, body: body, key: "key-12345", noCSRF: true}, &storeStub{canWrite: true}, 403, "csrf.invalid"},
		"no key":         {request{method: http.MethodPost, path: collection, body: body}, &storeStub{canWrite: true}, 400, "request.validation"},
		"short key":      {request{method: http.MethodPost, path: collection, body: body, key: "key"}, &storeStub{canWrite: true}, 400, "request.validation"},
		"unknown field":  {request{method: http.MethodPost, path: collection, body: `{"kind":"plan","text":"т","x":1}`, key: "key-12345"}, &storeStub{canWrite: true}, 400, "request.validation"},
		"bad kind":       {request{method: http.MethodPost, path: collection, body: `{"kind":"fact","text":"т"}`, key: "key-12345"}, &storeStub{canWrite: true}, 400, "request.validation"},
		"no text":        {request{method: http.MethodPost, path: collection, body: `{"kind":"plan","text":"  "}`, key: "key-12345"}, &storeStub{canWrite: true}, 400, "request.validation"},
		"reader":         {request{method: http.MethodPost, path: collection, body: body, key: "key-12345"}, &storeStub{}, 403, "forbidden"},
		"outsider":       {request{method: http.MethodPost, path: collection, body: `{"kind":"plan","text":"т","chapter":"99"}`, key: "key-12345"}, &storeStub{access: domain.ErrNotFound}, 404, "not_found"},
		"no chapter":     {request{method: http.MethodPost, path: collection, body: `{"kind":"plan","text":"т","chapter":"99"}`, key: "key-12345"}, &storeStub{canWrite: true}, 422, "author_memory.no_chapter"},
		"replay":         {request{method: http.MethodPost, path: collection, body: body, key: "key-12345"}, &storeStub{canWrite: true, err: domain.ErrConflict}, 409, "conflict"},
		"no revision":    {request{method: http.MethodPut, path: item, body: body}, &storeStub{canWrite: true}, 400, "request.validation"},
		"stale":          {request{method: http.MethodPut, path: item, body: `{"kind":"plan","text":"т","expectedRevision":1}`}, &storeStub{canWrite: true, err: domain.ErrConflict}, 409, "conflict"},
		"no such":        {request{method: http.MethodPut, path: item, body: `{"kind":"plan","text":"т","expectedRevision":1}`}, &storeStub{canWrite: true, err: domain.ErrNotFound}, 404, "not_found"},
		"reader change":  {request{method: http.MethodPut, path: item, body: `{"kind":"plan","text":"т","expectedRevision":1}`}, &storeStub{}, 403, "forbidden"},
		"delete bare":    {request{method: http.MethodDelete, path: item}, &storeStub{canWrite: true}, 400, "request.validation"},
		"reader delete":  {request{method: http.MethodDelete, path: item + "?expectedRevision=1"}, &storeStub{}, 403, "forbidden"},
		"delete no csrf": {request{method: http.MethodDelete, path: item + "?expectedRevision=1", noCSRF: true}, &storeStub{canWrite: true}, 403, "csrf.invalid"},
	} {
		w := serve(newRouter(test.store), test.input)
		if w.Code != test.code || problemCode(t, w) != test.kind {
			t.Fatalf("%s = %d %s, want %d %s", name, w.Code, w.Body.String(), test.code, test.kind)
		}
		if len(test.store.deleted) != 0 || test.code != 409 && test.code != 404 && (len(test.store.created) != 0 || len(test.store.updated) != 0) {
			t.Fatalf("%s reached the store: %+v", name, test.store)
		}
	}
	if w := serve(newRouter(&storeStub{canWrite: true}), request{method: http.MethodPost, path: collection, body: body, key: "key-12345", foreign: true}); w.Code != http.StatusForbidden {
		t.Fatalf("another origin = %d", w.Code)
	}
}

func TestWritesReachTheStore(t *testing.T) {
	item := collection + "/" + testRecord
	store := &storeStub{canWrite: true}
	router := newRouter(store)
	w := serve(router, request{method: http.MethodPost, path: collection, body: `{"kind":"plan","text":" Кира узнает правду ","chapter":"12","name":" Кира "}`, key: "key-12345"})
	if w.Code != http.StatusCreated || len(store.created) != 1 {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	command := store.created[0]
	if command.Draft != (domain.Draft{Kind: domain.KindPlan, Text: "Кира узнает правду", ChapterID: "chapter-1", Name: "Кира"}) ||
		command.ProjectID != testProject || command.Proof != testProof || command.KeyHash == "" || command.RequestHash == "" || command.ScopeHash == "" {
		t.Fatalf("command = %+v", command)
	}
	w = serve(router, request{method: http.MethodPut, path: item, body: `{"kind":"rejected","text":"нет","expectedRevision":3}`})
	if w.Code != http.StatusOK || store.updated[0] != (domain.Draft{Kind: domain.KindRejected, Text: "нет"}) || store.expected[0] != 3 {
		t.Fatalf("update = %d: %+v", w.Code, store)
	}
	w = serve(router, request{method: http.MethodDelete, path: item + "?expectedRevision=4"})
	if w.Code != http.StatusNoContent || store.deleted[0] != testRecord || store.expected[1] != 4 {
		t.Fatalf("delete = %d: %+v", w.Code, store)
	}
}

func TestAnUnexpectedFailureIsAServerError(t *testing.T) {
	w := serve(newRouter(&storeStub{err: errors.New("database")}), request{method: http.MethodGet, path: collection})
	if w.Code != http.StatusInternalServerError || problemCode(t, w) != "server.error" {
		t.Fatalf("failure = %d: %s", w.Code, w.Body.String())
	}
}

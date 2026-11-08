package library

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	service "canonkeeper/backend/internal/services/library"

	"github.com/go-chi/chi/v5"
)

type exportStore struct {
	head     domainlibrary.BookHead
	headErr  error
	chapters []domainlibrary.ExportChapter
	text     []string
}

func (store exportStore) BookHead(context.Context, domainlibrary.Actor, string) (domainlibrary.BookHead, error) {
	return store.head, store.headErr
}

func (store exportStore) BookExportChapters(context.Context, string, string) ([]domainlibrary.ExportChapter, error) {
	return store.chapters, nil
}

func (store exportStore) BookExportParagraphs(context.Context, string, string, domainlibrary.ExportChapter) ([]string, error) {
	return store.text, nil
}

type exportCover struct{}

func (exportCover) Execute(context.Context, domainlibrary.CoverLookup) (service.CoverContent, error) {
	return service.CoverContent{}, errors.New("no cover in this test")
}

func nowForExport() time.Time { return time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) }

func exportRouter(store exportStore, user exportUser) http.Handler {
	router := chi.NewRouter()
	MountBookExport(router, service.NewExportBook(store, exportCover{}, nowForExport), user)
	return router
}

type exportUser struct{ err error }

func (user exportUser) CurrentUser(context.Context, string) (auth.User, error) {
	return auth.User{ID: "actor"}, user.err
}

func (user exportUser) AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error) {
	return auth.User{ID: "actor"}, auth.SessionWriteProof{}, user.err
}

func exportFixture() exportStore {
	return exportStore{
		head:     domainlibrary.BookHead{ProjectID: "project", Title: `Том 1: «Порт»`, AuthorName: "Автор", CanEdit: true},
		chapters: []domainlibrary.ExportChapter{{ID: "one", Title: "Первая", Revision: 1}},
		text:     []string{"Текст."},
	}
}

func getExport(router http.Handler, query string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/books/book/export"+query, nil))
	return response
}

// E1.3: the file comes as an attachment named by its book as RFC 6266 wants, and nobody keeps a copy of it.
func TestBookExportAnswersWithAFile(t *testing.T) {
	for query, mediaType := range map[string]string{
		"?format=docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"?format=epub": "application/epub+zip",
		"?format=fb2":  "application/x-fictionbook+xml",
	} {
		response := getExport(exportRouter(exportFixture(), exportUser{}), query)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != mediaType || response.Body.Len() == 0 {
			t.Fatalf("%s: %d %q, %d bytes", query, response.Code, response.Header().Get("Content-Type"), response.Body.Len())
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: cache = %q", query, response.Header().Get("Cache-Control"))
		}
		disposition := response.Header().Get("Content-Disposition")
		if !strings.HasPrefix(disposition, "attachment; filename*=utf-8''") || !strings.Contains(disposition, "%D0%A2%D0%BE%D0%BC%201_%20") {
			t.Fatalf("%s: disposition = %q", query, disposition)
		}
	}
}

// E1.5, E1.7: every refusal has its code and leaves no half of a file behind.
func TestBookExportRefusesWithACode(t *testing.T) {
	stranger := exportFixture()
	stranger.headErr = domainlibrary.ErrNotFound
	viewer := exportFixture()
	viewer.head.CanEdit = false
	empty := exportFixture()
	empty.chapters = nil
	for name, test := range map[string]struct {
		store  exportStore
		user   exportUser
		query  string
		status int
		code   string
	}{
		"unknown format": {exportFixture(), exportUser{}, "?format=pdf", http.StatusBadRequest, "book_export.format"},
		"no format":      {exportFixture(), exportUser{}, "", http.StatusBadRequest, "book_export.format"},
		"no session":     {exportFixture(), exportUser{err: auth.ErrUnauthenticated}, "?format=docx", http.StatusUnauthorized, "auth.session_required"},
		"stranger":       {stranger, exportUser{}, "?format=docx", http.StatusNotFound, "not_found"},
		"viewer":         {viewer, exportUser{}, "?format=docx", http.StatusForbidden, "forbidden"},
		"empty book":     {empty, exportUser{}, "?format=docx", http.StatusUnprocessableEntity, "book_export.empty"},
	} {
		response := getExport(exportRouter(test.store, test.user), test.query)
		if response.Code != test.status || !strings.Contains(response.Body.String(), `"`+test.code+`"`) {
			t.Fatalf("%s: %d %s", name, response.Code, response.Body.String())
		}
		if response.Header().Get("Content-Disposition") != "" {
			t.Fatalf("%s: a refusal is named as a file", name)
		}
		if strings.Contains(response.Body.String(), "cover") {
			t.Fatalf("%s: the refusal speaks of a cover: %s", name, response.Body.String())
		}
	}
}

// E1.6: a file that broke halfway ends with a broken connection: the browser must not save half a book as a success.
func TestBookExportBreaksTheConnectionWhenTheFileCannotBeFinished(t *testing.T) {
	gone := exportFixture()
	gone.text = nil
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("recovered = %v", recovered)
		}
	}()
	getExport(exportRouter(gone, exportUser{}), "?format=fb2")
	t.Fatal("the handler finished as if the file were whole")
}

// exportLateFailure gives the text of the first chapter and fails on the second, when the status has long gone.
type exportLateFailure struct{ exportStore }

func (store exportLateFailure) BookExportParagraphs(_ context.Context, _, _ string, chapter domainlibrary.ExportChapter) ([]string, error) {
	if chapter.ID == "two" {
		return nil, errors.New("the chapter is gone")
	}
	return []string{strings.Repeat("Длинный текст главы, чтобы ответ успел уйти. ", 4000)}, nil
}

// E1.6, on a real connection: what the browser gets of a file that broke halfway is an error, not a short file.
func TestBookExportFailureReachesTheClientAsABrokenDownload(t *testing.T) {
	store := exportFixture()
	store.chapters = []domainlibrary.ExportChapter{{ID: "one", Title: "Первая", Revision: 1}, {ID: "two", Title: "Вторая", Revision: 1}}
	router := chi.NewRouter()
	MountBookExport(router, service.NewExportBook(exportLateFailure{store}, exportCover{}, nowForExport), exportUser{})
	server := httptest.NewServer(router)
	defer server.Close()

	response, err := http.Get(server.URL + "/books/book/export?format=fb2")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err == nil {
		t.Fatalf("the client read %d bytes as a whole file", len(body))
	}
	if strings.Contains(string(body), "</FictionBook>") {
		t.Fatal("the file ended as if it were whole")
	}
}

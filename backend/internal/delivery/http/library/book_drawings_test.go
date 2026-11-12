package library

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	service "canonkeeper/backend/internal/services/library"

	"github.com/go-chi/chi/v5"
)

// drawingsPort is the drawings of a site: one picture after the first paragraph, and whether it was asked.
type drawingsPort struct{ asked *int }

func (port drawingsPort) Pictures(context.Context, string, []domainlibrary.ExportChapter) ([]service.BookPicture, error) {
	*port.asked++
	var content bytes.Buffer
	if err := png.Encode(&content, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		return nil, err
	}
	return []service.BookPicture{{Chapter: 0, After: 1, Name: "Мара у пристани", Read: func(context.Context) ([]byte, string, error) {
		return content.Bytes(), "image/png", nil
	}}}, nil
}

// specs/2026-11-12-ux10-book-pictures.md BP2.1: the file takes the drawings of the book by drawings=true alone; without it
// the port is not even asked; a value the contract does not have is refused before the file.
func TestBookExportTakesTheDrawingsByItsParameter(t *testing.T) {
	asked := 0
	router := chi.NewRouter()
	MountBookExport(router, service.NewExportBook(exportFixture(), exportCover{}, nowForExport).WithPictures(drawingsPort{asked: &asked}), exportUser{})
	for query, want := range map[string]bool{"?format=fb2": false, "?format=fb2&drawings=false": false, "?format=fb2&drawings=true": true} {
		asked = 0
		response := getExport(router, query)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `alt="Мара у пристани"`) != want || (asked == 1) != want {
			t.Fatalf("%s: %d, asked %d, %.300s", query, response.Code, asked, response.Body.String())
		}
	}
	if response := getExport(router, "?format=fb2&drawings=yes"); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"request.validation"`) {
		t.Fatalf("drawings=yes: %d %s", response.Code, response.Body.String())
	}
}

type countsStore struct {
	canEdit bool
	err     error
}

func (store countsStore) ProjectCanEdit(context.Context, domainlibrary.Actor, string) (bool, error) {
	return store.canEdit, store.err
}

func (store countsStore) ProjectExportChapters(context.Context, string) ([]domainlibrary.ExportChapter, error) {
	return []domainlibrary.ExportChapter{{ID: "one", BookID: "book-1", Paragraphs: 2}}, nil
}

// BP3.1, BP2.5: the counts of the drawings of the books answer an editor; a viewer, a stranger and a guest get theirs.
func TestBookDrawingCountsAnswerAnEditor(t *testing.T) {
	get := func(store countsStore, user exportUser) *httptest.ResponseRecorder {
		router := chi.NewRouter()
		asked := 0
		MountBookDrawingCounts(router, service.NewCountBookDrawings(store, drawingsPort{asked: &asked}), user)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/projects/project/books/drawing-counts", nil))
		return response
	}
	response := get(countsStore{canEdit: true}, exportUser{})
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"data":[{"bookId":"book-1","drawings":1}]}` ||
		response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("an editor: %d %s", response.Code, response.Body.String())
	}
	for name, test := range map[string]struct {
		store  countsStore
		user   exportUser
		status int
		code   string
	}{
		"viewer":     {countsStore{}, exportUser{}, http.StatusForbidden, "forbidden"},
		"stranger":   {countsStore{err: domainlibrary.ErrNotFound}, exportUser{}, http.StatusNotFound, "not_found"},
		"no session": {countsStore{canEdit: true}, exportUser{err: auth.ErrUnauthenticated}, http.StatusUnauthorized, "auth.session_required"},
	} {
		if response := get(test.store, test.user); response.Code != test.status || !strings.Contains(response.Body.String(), `"`+test.code+`"`) {
			t.Fatalf("%s: %d %s", name, response.Code, response.Body.String())
		}
	}
}

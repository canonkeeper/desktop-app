package library

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"canonkeeper/backend/internal/domain/bookexport"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	service "canonkeeper/backend/internal/services/library"

	"github.com/go-chi/chi/v5"
)

// BookExportUseCase prepares the file of a book.
type BookExportUseCase interface {
	Execute(context.Context, service.ExportBookInput) (service.BookFile, error)
}

// MountBookExport serves getBookExport: a GET without a write, so it needs a session and no request-forgery token.
func MountBookExport(router chi.Router, export BookExportUseCase, currentUser CurrentUserService) {
	if export == nil || currentUser == nil {
		panic("book export HTTP: dependencies are required")
	}
	authentication := Handlers{currentUser: currentUser}
	router.Get("/books/{bookId}/export", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		actor, ok := authentication.actor(response, request)
		if !ok {
			return
		}
		// The drawings of the book go in only when asked (specs/2026-11-12-ux10-book-pictures.md BP2.1).
		drawings := request.URL.Query().Get("drawings")
		if drawings != "" && drawings != "true" && drawings != "false" {
			writeCoverProblem(response, http.StatusBadRequest, "Bad Request", "drawings must be true or false", "request.validation")
			return
		}
		file, err := export.Execute(request.Context(), service.ExportBookInput{
			Actor: actor, BookID: chi.URLParam(request, "bookId"), Format: request.URL.Query().Get("format"), Drawings: drawings == "true",
		})
		switch {
		case errors.Is(err, bookexport.ErrUnknownFormat):
			writeCoverProblem(response, http.StatusBadRequest, "Bad Request", "format must be docx, epub, or fb2", "book_export.format")
			return
		case errors.Is(err, bookexport.ErrEmptyBook):
			writeCoverProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "the book has no text to export", "book_export.empty")
			return
		case errors.Is(err, domainlibrary.ErrNotFound):
			writeCoverProblem(response, http.StatusNotFound, "Not Found", "book not found", "not_found")
			return
		case errors.Is(err, domainlibrary.ErrForbidden):
			writeCoverProblem(response, http.StatusForbidden, "Forbidden", "only an editor may download a whole book", "forbidden")
			return
		case err != nil:
			writeCoverError(response, err)
			return
		}
		response.Header().Set("Content-Type", file.MediaType)
		// The standard library names a non-ASCII file as RFC 6266 wants: filename*=utf-8''…
		response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
		response.WriteHeader(http.StatusOK)
		if err := file.Write(response); err != nil {
			// The status has gone. Only a broken connection tells the browser that the file is not whole.
			panic(http.ErrAbortHandler)
		}
	})
}

// BookDrawingCountsUseCase counts the drawings standing in the books of a saga.
type BookDrawingCountsUseCase interface {
	Execute(context.Context, domainlibrary.Actor, string) ([]service.BookDrawingCount, error)
}

type bookDrawingCountDTO struct {
	BookID   string `json:"bookId"`
	Drawings int    `json:"drawings"`
}

// MountBookDrawingCounts serves getBookDrawingCounts (specs/2026-11-12-ux10-book-pictures.md BP3.1, BP3.2): how many
// drawings the file of each book of a saga would carry. A read with the rights of the file of a book: a session, no
// request-forgery token. Only a build with drawings mounts it.
func MountBookDrawingCounts(router chi.Router, count BookDrawingCountsUseCase, currentUser CurrentUserService) {
	if count == nil || currentUser == nil {
		panic("book drawing counts HTTP: dependencies are required")
	}
	authentication := Handlers{currentUser: currentUser}
	router.Get("/projects/{projectId}/books/drawing-counts", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		actor, ok := authentication.actor(response, request)
		if !ok {
			return
		}
		counts, err := count.Execute(request.Context(), actor, chi.URLParam(request, "projectId"))
		switch {
		case errors.Is(err, domainlibrary.ErrNotFound):
			writeCoverProblem(response, http.StatusNotFound, "Not Found", "saga not found", "not_found")
			return
		case errors.Is(err, domainlibrary.ErrForbidden):
			writeCoverProblem(response, http.StatusForbidden, "Forbidden", "only an editor may download the books", "forbidden")
			return
		case err != nil:
			writeCoverError(response, err)
			return
		}
		data := make([]bookDrawingCountDTO, len(counts))
		for index, book := range counts {
			data[index] = bookDrawingCountDTO{BookID: book.BookID, Drawings: book.Drawings}
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"data": data})
	})
}

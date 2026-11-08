package library

import (
	"context"
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
		file, err := export.Execute(request.Context(), service.ExportBookInput{
			Actor: actor, BookID: chi.URLParam(request, "bookId"), Format: request.URL.Query().Get("format"),
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

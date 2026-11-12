package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/library"

	"github.com/go-chi/chi/v5"
)

// PreviewBookRevertUseCase tells what a return of a book to the version before its update would do.
type PreviewBookRevertUseCase interface {
	Execute(context.Context, domainlibrary.Actor, string) (domainlibrary.BookRevertPreview, error)
}

// RevertBookUpdateUseCase returns a book to the version before its last update from a file.
type RevertBookUpdateUseCase interface {
	Execute(context.Context, service.RevertBookUpdateInput) (string, error)
}

// BookReadFunc is a book as listBooks gives it, for the answer of a write.
type BookReadFunc func(ctx context.Context, actor domainlibrary.Actor, bookID string) (any, error)

// MountBookRevert serves previewBookUpdateRevert, a read under the session, and revertBookUpdate, a write under the
// session, the request-forgery token and the Origin (specs/2026-11-12-ux10-book-revert.md BR2).
func MountBookRevert(router chi.Router, preview PreviewBookRevertUseCase, revert RevertBookUpdateUseCase, book BookReadFunc, currentUser CurrentUserService, origins []string) {
	if preview == nil || revert == nil || book == nil || currentUser == nil {
		panic("book revert HTTP: dependencies are required")
	}
	handlers := Handlers{currentUser: currentUser, allowedOrigins: origins}
	router.Get("/books/{bookId}/update-revert", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		actor, ok := handlers.actor(response, request)
		if !ok {
			return
		}
		if request.URL.RawQuery != "" {
			writeCoverProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
			return
		}
		found, err := preview.Execute(request.Context(), actor, chi.URLParam(request, "bookId"))
		if err != nil {
			writeRevertError(response, err)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(presentRevertPreview(found))
	})
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(origins...))
		router.Use(handlers.csrfMiddleware)
		router.Post("/books/{bookId}/update-revert", func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("Cache-Control", "no-store")
			actor, ok := handlers.actor(response, request)
			if !ok {
				return
			}
			var body struct {
				Fingerprint string `json:"fingerprint"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1024))
			decoder.DisallowUnknownFields()
			if request.URL.RawQuery != "" || decoder.Decode(&body) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || body.Fingerprint == "" {
				writeCoverProblem(response, http.StatusBadRequest, "Bad Request", "fingerprint is required", "request.validation")
				return
			}
			bookID := chi.URLParam(request, "bookId")
			if _, err := revert.Execute(request.Context(), service.RevertBookUpdateInput{Actor: actor, BookID: bookID, Fingerprint: body.Fingerprint}); err != nil {
				writeRevertError(response, err)
				return
			}
			returned, err := book(request.Context(), actor, bookID)
			if err != nil {
				writeRevertError(response, err)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(returned)
		})
	})
}

type revertChapterResponse struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	PreviousTitle string `json:"previousTitle,omitempty"`
	Stay          string `json:"stay,omitempty"`
}

type revertPreviewResponse struct {
	BookID      string                  `json:"bookId"`
	BookTitle   string                  `json:"bookTitle"`
	UpdatedAt   string                  `json:"updatedAt"`
	RevertUntil string                  `json:"revertUntil"`
	Fingerprint string                  `json:"fingerprint"`
	Text        []revertChapterResponse `json:"text"`
	Place       []revertChapterResponse `json:"place"`
	Returned    []revertChapterResponse `json:"returned"`
	Removed     []revertChapterResponse `json:"removed"`
	Kept        []revertChapterResponse `json:"kept"`
}

func presentRevertPreview(preview domainlibrary.BookRevertPreview) revertPreviewResponse {
	chapters := func(items []domainlibrary.RevertChapter) []revertChapterResponse {
		shown := make([]revertChapterResponse, 0, len(items))
		for _, item := range items {
			shown = append(shown, revertChapterResponse{ID: item.ID, Title: item.Title, PreviousTitle: item.PreviousTitle, Stay: string(item.Stay)})
		}
		return shown
	}
	moment := func(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000Z07:00") }
	return revertPreviewResponse{
		BookID: preview.BookID, BookTitle: preview.BookTitle, UpdatedAt: moment(preview.UpdatedAt), RevertUntil: moment(preview.RevertUntil),
		Fingerprint: preview.Fingerprint, Text: chapters(preview.Text), Place: chapters(preview.Place),
		Returned: chapters(preview.Returned), Removed: chapters(preview.Removed), Kept: chapters(preview.Kept),
	}
}

// writeRevertError answers a refusal of a return: a limit of the tier and the demo saga in the words of the quotas, a
// book being updated, a book without an update to return from and a book changed after its preview by their codes.
func writeRevertError(response http.ResponseWriter, err error) {
	if httpapi.WriteQuotaProblem(response, err, time.Now()) {
		return
	}
	switch {
	case errors.Is(err, domainlibrary.ErrUpdateGone):
		writeCoverProblem(response, http.StatusNotFound, "Not Found", "the book has no update to return from", "book.update_gone")
	case errors.Is(err, domainlibrary.ErrBookBusy):
		writeCoverProblem(response, http.StatusConflict, "Conflict", "the book is being updated from a file", "book.busy")
	case errors.Is(err, domainlibrary.ErrUpdateChanged):
		writeCoverProblem(response, http.StatusConflict, "Conflict", "the book changed after the preview", "book.update_changed")
	case errors.Is(err, domainlibrary.ErrNotFound):
		writeCoverProblem(response, http.StatusNotFound, "Not Found", "book not found", "not_found")
	case errors.Is(err, domainlibrary.ErrForbidden):
		writeCoverProblem(response, http.StatusForbidden, "Forbidden", "only an editor may return a book", "forbidden")
	case errors.Is(err, domainlibrary.ErrValidation):
		writeCoverProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	case errors.Is(err, domainlibrary.ErrConflict):
		writeCoverProblem(response, http.StatusConflict, "Conflict", "the book changed concurrently", "conflict")
	default:
		writeCoverError(response, err)
	}
}

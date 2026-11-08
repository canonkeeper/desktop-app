package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/library"

	"github.com/go-chi/chi/v5"
)

// CoverFromImageUseCase makes a drawing of the saga the cover of a book.
type CoverFromImageUseCase interface {
	Execute(context.Context, service.SetCoverFromImageInput) (domainlibrary.CoverActivation, error)
}

// MountCoverFromImage serves setBookCoverFromImage: a write under the same guards as an upload of a cover.
func MountCoverFromImage(router chi.Router, useCase CoverFromImageUseCase, bookResponse BookResponseFunc, currentUser CurrentUserService, origins []string) {
	if useCase == nil || bookResponse == nil || currentUser == nil {
		panic("book cover from a drawing HTTP: dependencies are required")
	}
	handlers := Handlers{bookResponse: bookResponse, currentUser: currentUser, allowedOrigins: origins}
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(origins...))
		router.Use(handlers.csrfMiddleware)
		router.Post("/books/{bookId}/cover/from-image", func(response http.ResponseWriter, request *http.Request) {
			actor, ok := handlers.actor(response, request)
			if !ok {
				return
			}
			var body struct {
				ImageNumber int `json:"imageNumber"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1024))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&body) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || body.ImageNumber < 1 {
				writeCoverProblem(response, http.StatusBadRequest, "Bad Request", "imageNumber is required", "request.validation")
				return
			}
			bookID := chi.URLParam(request, "bookId")
			activation, err := useCase.Execute(request.Context(), service.SetCoverFromImageInput{Actor: actor, BookID: bookID, ImageNumber: body.ImageNumber})
			if errors.Is(err, domainlibrary.ErrNotFound) {
				writeCoverProblem(response, http.StatusNotFound, "Not Found", "the book or the drawing is not found", "not_found")
				return
			}
			if err != nil {
				writeCoverError(response, err)
				return
			}
			coverURL := "/api/v1/books/" + bookID + "/cover/" + strconv.FormatInt(activation.Current.Version, 10)
			handlers.writeBook(response, request, actor, bookID, &coverURL)
		})
	})
}

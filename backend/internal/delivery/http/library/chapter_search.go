package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	domain "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/httpapi"
	"github.com/go-chi/chi/v5"
)

type ChapterSearchUseCase interface {
	Execute(context.Context, domain.Actor, domain.ChapterSearchRequest) (domain.ChapterSearchPage, error)
}

func MountChapterSearch(router chi.Router, search ChapterSearchUseCase, currentUser CurrentUserService, origins []string) {
	if search == nil || currentUser == nil {
		panic("chapter search HTTP: dependencies required")
	}
	authentication := Handlers{currentUser: currentUser, allowedOrigins: origins}
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginRequiredMiddleware(origins...))
		router.Use(authentication.csrfMiddleware)
		router.Post("/chapters/search", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			actor, ok := authentication.actor(w, r)
			if !ok {
				return
			}
			var request struct {
				Query      string          `json:"query"`
				SourceView string          `json:"sourceView"`
				Limit      json.RawMessage `json:"limit"`
				Cursor     json.RawMessage `json:"cursor"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
			decoder.DisallowUnknownFields()
			if r.URL.RawQuery != "" || decoder.Decode(&request) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
				writeCoverProblem(w, http.StatusBadRequest, "Bad Request", "invalid chapter search request", "request.validation")
				return
			}
			input := domain.ChapterSearchRequest{Query: request.Query, SourceView: request.SourceView}
			if len(request.Limit) > 0 && (json.Unmarshal(request.Limit, &input.Limit) != nil || input.Limit < 1 || input.Limit > 50) ||
				len(request.Cursor) > 0 && (json.Unmarshal(request.Cursor, &input.Cursor) != nil || input.Cursor == "" || len(input.Cursor) > 512) {
				writeCoverProblem(w, http.StatusBadRequest, "Bad Request", "invalid chapter search request", "request.validation")
				return
			}
			page, err := search.Execute(r.Context(), actor, input)
			if err != nil {
				if errors.Is(err, domain.ErrValidation) {
					writeCoverProblem(w, http.StatusBadRequest, "Bad Request", "invalid chapter search request", "request.validation")
				} else {
					writeCoverError(w, err)
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(page)
		})
	})
}

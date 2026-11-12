// Package trash serves listRecentlyDeleted (specs/2026-11-11-ux7-trash.md TB1.1): «Недавно удалённые» of the signed-in
// author over all his sagas.
package trash

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/trash"
	"canonkeeper/backend/internal/httpapi"

	"github.com/go-chi/chi/v5"
)

type CurrentUser interface {
	CurrentUser(context.Context, string) (auth.User, error)
}

type List interface {
	Execute(ctx context.Context, actorUserID string) (trash.List, error)
}

type Handlers struct {
	List List
	Auth CurrentUser
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Get("/recently-deleted", handlers.listRecentlyDeleted)
}

type group[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

type saga struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Books        int    `json:"books"`
	DeletedAt    string `json:"deletedAt"`
	RestoreUntil string `json:"restoreUntil"`
}

type book struct {
	ID           string `json:"id"`
	ProjectID    string `json:"projectId"`
	SagaTitle    string `json:"sagaTitle"`
	Title        string `json:"title"`
	DeletedAt    string `json:"deletedAt"`
	RestoreUntil string `json:"restoreUntil"`
}

type chapter struct {
	ID                string `json:"id"`
	ProjectID         string `json:"projectId"`
	SagaTitle         string `json:"sagaTitle"`
	BookID            string `json:"bookId"`
	BookTitle         string `json:"bookTitle"`
	BookDisplayNumber string `json:"bookDisplayNumber"`
	Title             string `json:"title"`
	Order             int    `json:"order"`
	DeletedAt         string `json:"deletedAt"`
	RestoreUntil      string `json:"restoreUntil"`
}

type bookUpdate struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	SagaTitle   string `json:"sagaTitle"`
	Title       string `json:"title"`
	UpdatedAt   string `json:"updatedAt"`
	RevertUntil string `json:"revertUntil"`
}

type drawing struct {
	ID           string `json:"id"`
	ProjectID    string `json:"projectId"`
	SagaTitle    string `json:"sagaTitle"`
	Title        string `json:"title"`
	Number       *int   `json:"number,omitempty"`
	DeletedAt    string `json:"deletedAt"`
	RestoreUntil string `json:"restoreUntil"`
}

type response struct {
	Sagas       group[saga]       `json:"sagas"`
	Books       group[book]       `json:"books"`
	Chapters    group[chapter]    `json:"chapters"`
	BookUpdates group[bookUpdate] `json:"bookUpdates"`
	Drawings    *group[drawing]   `json:"drawings,omitempty"`
}

func (handlers Handlers) listRecentlyDeleted(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
		return
	}
	user, err := handlers.Auth.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if err != nil {
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
		return
	}
	list, err := handlers.List.Execute(r.Context(), user.ID)
	if err != nil {
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(present(list))
}

func present(list trash.List) response {
	value := response{
		Sagas: each(list.Sagas, func(item trash.Saga) saga {
			return saga{item.ID, item.Title, item.Books, at(item.DeletedAt), at(item.RestoreUntil)}
		}),
		Books: each(list.Books, func(item trash.Book) book {
			return book{item.ID, item.ProjectID, item.SagaTitle, item.Title, at(item.DeletedAt), at(item.RestoreUntil)}
		}),
		Chapters: each(list.Chapters, func(item trash.Chapter) chapter {
			return chapter{item.ID, item.ProjectID, item.SagaTitle, item.BookID, item.BookTitle, item.BookDisplayNumber, item.Title, item.Order, at(item.DeletedAt), at(item.RestoreUntil)}
		}),
		BookUpdates: each(list.BookUpdates, func(item trash.BookUpdate) bookUpdate {
			return bookUpdate{item.BookID, item.ProjectID, item.SagaTitle, item.Title, at(item.UpdatedAt), at(item.RevertUntil)}
		}),
	}
	if list.Drawings != nil {
		drawings := each(*list.Drawings, func(item trash.Drawing) drawing {
			shown := drawing{ID: item.ID, ProjectID: item.ProjectID, SagaTitle: item.SagaTitle, Title: item.Title, DeletedAt: at(item.DeletedAt), RestoreUntil: at(item.RestoreUntil)}
			if item.Number > 0 {
				number := item.Number
				shown.Number = &number
			}
			return shown
		})
		value.Drawings = &drawings
	}
	return value
}

func each[T, V any](source trash.Group[T], present func(T) V) group[V] {
	items := make([]V, 0, len(source.Items))
	for _, item := range source.Items {
		items = append(items, present(item))
	}
	return group[V]{Items: items, Total: source.Total}
}

func at(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

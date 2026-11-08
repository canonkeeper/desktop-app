package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"canonkeeper/backend/internal/auth"
	indexingdomain "canonkeeper/backend/internal/domain/indexing"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"
	indexingservice "canonkeeper/backend/internal/services/indexing"

	"github.com/go-chi/chi/v5"
)

type CurrentUser interface {
	CurrentUser(context.Context, string) (auth.User, error)
}

type StatusHandler struct {
	service     *indexingservice.Service
	currentUser CurrentUser
}

func NewStatusHandler(service *indexingservice.Service, currentUser CurrentUser) StatusHandler {
	if service == nil || currentUser == nil {
		panic("indexing HTTP dependencies are required")
	}
	return StatusHandler{service: service, currentUser: currentUser}
}

func (handler StatusHandler) Mount(router chi.Router) {
	router.Get("/projects/{projectId}/indexing-status", handler.Get)
}

func (handler StatusHandler) Get(w http.ResponseWriter, request *http.Request) {
	user, err := handler.currentUser.CurrentUser(request.Context(), auth.SessionCookieValue(request))
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	bookID, sourceView, err := statusScope(request)
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	status, err := handler.service.GetStatus(request.Context(), indexingservice.GetStatusInput{
		ActorUserID: user.ID, ProjectID: strings.TrimSpace(chi.URLParam(request, "projectId")),
		BookID: bookID, SourceView: sourceView,
	})
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func statusScope(request *http.Request) (string, indexingdomain.SourceView, error) {
	query := request.URL.Query()
	books, booksOK := query["bookId"]
	views, viewsOK := query["sourceView"]
	if !booksOK && !viewsOK {
		return "", "", nil
	}
	if !booksOK || !viewsOK || len(books) != 1 || len(views) != 1 {
		return "", "", indexingservice.ErrInvalid
	}
	bookID := strings.TrimSpace(books[0])
	view := indexingdomain.SourceView(strings.TrimSpace(views[0]))
	if bookID == "" || view != indexingdomain.SourceDraft && view != indexingdomain.SourcePublished {
		return "", "", indexingservice.ErrInvalid
	}
	return bookID, view, nil
}

func writeError(w http.ResponseWriter, ctx context.Context, err error) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, indexingdomain.ErrNotFound):
		httpapi.WriteProblem(w, "", http.StatusNotFound, "Not Found", "indexing status not found", "not_found")
	case errors.Is(err, indexingdomain.ErrDenied):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "indexing access denied", "forbidden")
	case errors.Is(err, indexingservice.ErrInvalid), errors.Is(err, indexingdomain.ErrValidation):
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	case errors.Is(err, indexingdomain.ErrConflict):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "indexing state conflict", "conflict")
	default:
		observability.ReportUnexpectedError(ctx, err)
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

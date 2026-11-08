package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"

	"github.com/go-chi/chi/v5"
)

type JobService interface {
	ListProjectJobs(context.Context, Actor, string, ListProjectJobsInput) (JobPage, error)
	GetJob(context.Context, Actor, string) (Job, error)
	CancelJob(context.Context, Actor, string) (CancelResult, error)
}

type CurrentUserService interface {
	CurrentUser(context.Context, string) (auth.User, error)
	ValidateCSRF(context.Context, string, string) error
}

type Handlers struct {
	service        JobService
	currentUser    CurrentUserService
	allowedOrigins []string
}

type actorUserIDContextKey struct{}

func NewHandlers(service JobService, currentUser CurrentUserService, allowedOrigins []string) Handlers {
	if service == nil {
		panic("jobs: JobService is required")
	}
	if currentUser == nil {
		panic("jobs: CurrentUserService is required")
	}
	return Handlers{
		service:        service,
		currentUser:    currentUser,
		allowedOrigins: slices.Clone(allowedOrigins),
	}
}

func (h Handlers) Mount(router chi.Router) {
	router.Get("/projects/{projectId}/jobs", h.ListProjectJobs)
	router.Get("/jobs/{jobId}", h.GetJob)
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(h.allowedOrigins...))
		router.Use(h.CSRFMiddleware)
		router.Post("/jobs/{jobId}/cancel", h.CancelJob)
	})
}

func (h Handlers) ListProjectJobs(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	input, err := listProjectJobsInputFromRequest(r)
	if err != nil {
		writeJobServiceError(w, r.Context(), err)
		return
	}
	page, err := h.service.ListProjectJobs(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "projectId"), input)
	if err != nil {
		writeJobServiceError(w, r.Context(), err)
		return
	}
	writeJobListResponse(w, http.StatusOK, page, actorUserID)
}

func (h Handlers) GetJob(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	job, err := h.service.GetJob(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "jobId"))
	if err != nil {
		writeJobServiceError(w, r.Context(), err)
		return
	}
	writeJobResponse(w, http.StatusOK, job, actorUserID)
}

func (h Handlers) CancelJob(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	result, err := h.service.CancelJob(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "jobId"))
	if err != nil {
		writeJobServiceError(w, r.Context(), err)
		return
	}
	if result.Opaque {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJobResponse(w, http.StatusOK, result.Job, actorUserID)
}

func (h Handlers) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpapi.RequiresCSRF(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		cookieValue := auth.SessionCookieValue(r)
		user, err := h.currentUser.CurrentUser(r.Context(), cookieValue)
		if err != nil {
			writeJobServiceError(w, r.Context(), err)
			return
		}
		csrfToken := r.Header.Get("X-CSRF-Token")
		if csrfToken == "" {
			writeJobProblem(w, http.StatusForbidden, "Forbidden", "missing CSRF token", "csrf.required")
			return
		}
		if err := h.currentUser.ValidateCSRF(r.Context(), cookieValue, csrfToken); err != nil {
			if errors.Is(err, auth.ErrInvalidCSRF) {
				writeJobProblem(w, http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
				return
			}
			writeJobProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
			return
		}
		ctx := context.WithValue(r.Context(), actorUserIDContextKey{}, user.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h Handlers) actorUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if actorUserID, ok := r.Context().Value(actorUserIDContextKey{}).(string); ok {
		return actorUserID, true
	}
	user, err := h.currentUser.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if err != nil {
		writeJobServiceError(w, r.Context(), err)
		return "", false
	}
	return user.ID, true
}

func writeJobResponse(w http.ResponseWriter, status int, job Job, viewerUserID string) {
	w.Header().Set("Cache-Control", "no-store")
	response, err := jobResponseForViewer(job, jobSupportCode(job), viewerUserID)
	if err != nil {
		writeJobProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
		return
	}
	writeJobJSON(w, status, response)
}

func writeJobListResponse(w http.ResponseWriter, status int, page JobPage, viewerUserID string) {
	data := make([]jobResponse, len(page.Jobs))
	for i, job := range page.Jobs {
		response, err := jobResponseForViewer(job, jobSupportCode(job), viewerUserID)
		if err != nil {
			writeJobProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
			return
		}
		data[i] = response
	}
	var nextCursor *string
	if page.Meta.NextCursor != "" {
		cursor := page.Meta.NextCursor
		nextCursor = &cursor
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJobJSON(w, status, jobListResponse{Data: data, Meta: pageMetaResponse{HasMore: page.Meta.HasMore, NextCursor: nextCursor}})
}

func listProjectJobsInputFromRequest(r *http.Request) (ListProjectJobsInput, error) {
	query := r.URL.Query()
	kind := Kind(query.Get("kind"))
	if kind != "" && !validKind(kind) {
		return ListProjectJobsInput{}, ErrValidation
	}
	input := ListProjectJobsInput{Kind: kind, Limit: defaultPageLimit, Cursor: query.Get("cursor")}
	switch query.Get("active") {
	case "", "false":
	case "true":
		input.ActiveOnly = true
	default:
		return ListProjectJobsInput{}, ErrValidation
	}
	limitValue := query.Get("limit")
	if limitValue == "" {
		return input, nil
	}
	limit, err := strconv.Atoi(limitValue)
	if err != nil || limit < 1 || limit > maxPageLimit {
		return ListProjectJobsInput{}, ErrValidation
	}
	input.Limit = limit
	return input, nil
}

func validKind(kind Kind) bool {
	return slices.Contains(currentKinds[:], kind)
}

func writeJobServiceError(w http.ResponseWriter, ctx context.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeJobProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, ErrNotFound):
		writeJobProblem(w, http.StatusNotFound, "Not Found", "job not found", "not_found")
	case errors.Is(err, ErrForbidden):
		writeJobProblem(w, http.StatusForbidden, "Forbidden", "job access denied", "forbidden")
	case errors.Is(err, ErrValidation):
		writeJobProblem(w, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	case errors.Is(err, ErrConflict):
		writeJobProblem(w, http.StatusConflict, "Conflict", "request conflicts with current job state", "conflict")
	default:
		observability.ReportUnexpectedError(ctx, err)
		writeJobProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

func writeJobJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJobProblem(w http.ResponseWriter, status int, title, detail, code string) {
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(w, "", status, title, detail, code)
}

func formatJobTime(value time.Time) string {
	return value.Format("2006-01-02T15:04:05.000Z07:00")
}

package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/observability"

	"github.com/go-chi/chi/v5"
)

type StartService interface {
	StartIndexing(context.Context, Actor, StartIndexingInput) (jobs.Job, error)
}

type CurrentUserService interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}

type Handlers struct {
	service        StartService
	currentUser    CurrentUserService
	allowedOrigins []string
}

type actorUserIDContextKey struct{}
type sessionWriteProofContextKey struct{}

func NewHandlers(service StartService, currentUser CurrentUserService, allowedOrigins []string) Handlers {
	if service == nil {
		panic("indexing: StartService is required")
	}
	if currentUser == nil {
		panic("indexing: CurrentUserService is required")
	}
	return Handlers{
		service:        service,
		currentUser:    currentUser,
		allowedOrigins: slices.Clone(allowedOrigins),
	}
}

func (h Handlers) Mount(router chi.Router) {
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(h.allowedOrigins...))
		router.Use(h.CSRFMiddleware)
		router.Post("/projects/{projectId}/indexing-runs", h.StartIndexing)
	})
}

func (h Handlers) StartIndexing(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	input, err := startIndexingInputFromRequest(r)
	if err != nil {
		writeIndexingServiceError(w, r.Context(), err)
		return
	}
	input.ProjectID = chi.URLParam(r, "projectId")
	input.RequestMethod = r.Method
	input.RequestPath = r.URL.Path
	input.SessionWriteProof = sessionWriteProofFromContext(r.Context())
	job, err := h.service.StartIndexing(r.Context(), Actor{UserID: actorUserID}, input)
	if err != nil {
		writeIndexingServiceError(w, r.Context(), err)
		return
	}
	if err := writeJobStartResponse(w, r.Context(), job); err != nil {
		writeIndexingServiceError(w, r.Context(), err)
	}
}

func (h Handlers) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpapi.RequiresCSRF(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		cookieValue := auth.SessionCookieValue(r)
		csrfToken := r.Header.Get("X-CSRF-Token")
		user, proof, err := h.currentUser.AuthenticateWrite(r.Context(), cookieValue, csrfToken)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCSRF) {
				if csrfToken == "" {
					writeIndexingProblem(w, http.StatusForbidden, "Forbidden", "missing CSRF token", "csrf.required")
				} else {
					writeIndexingProblem(w, http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
				}
				return
			}
			writeIndexingServiceError(w, r.Context(), err)
			return
		}
		ctx := context.WithValue(r.Context(), actorUserIDContextKey{}, user.ID)
		ctx = context.WithValue(ctx, sessionWriteProofContextKey{}, proof)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func sessionWriteProofFromContext(ctx context.Context) auth.SessionWriteProof {
	proof, _ := ctx.Value(sessionWriteProofContextKey{}).(auth.SessionWriteProof)
	return proof
}

func (h Handlers) actorUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if actorUserID, ok := r.Context().Value(actorUserIDContextKey{}).(string); ok {
		return actorUserID, true
	}
	user, err := h.currentUser.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if err != nil {
		writeIndexingServiceError(w, r.Context(), err)
		return "", false
	}
	return user.ID, true
}

// startProjectIndexingRequest restarts the text index of one text version; its success
// queues the cards of the chapters that still have none.
type startProjectIndexingRequest struct {
	SourceView governance.SourceView `json:"sourceView"`
}

func startIndexingInputFromRequest(r *http.Request) (StartIndexingInput, error) {
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) < minIdempotencyKeyLength || len(idempotencyKey) > maxIdempotencyKeyLength {
		return StartIndexingInput{}, ErrValidation
	}
	var requestBody startProjectIndexingRequest
	decoder := json.NewDecoder(httpapi.JSONBody(r))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&requestBody); err != nil {
		return StartIndexingInput{}, ErrValidation
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return StartIndexingInput{}, ErrValidation
	}
	if requestBody.SourceView != governance.SourceViewDraft && requestBody.SourceView != governance.SourceViewPublished {
		return StartIndexingInput{}, ErrValidation
	}
	return StartIndexingInput{SourceView: requestBody.SourceView, IdempotencyKey: idempotencyKey}, nil
}

type jobStartResponse struct {
	JobID string                 `json:"jobId"`
	Job   jobs.PublicJobResponse `json:"job"`
	Links jobStartLinks          `json:"links"`
}

type jobStartLinks struct {
	Job string `json:"job"`
}

func writeJobStartResponse(w http.ResponseWriter, ctx context.Context, job jobs.Job) error {
	requestID, _ := observability.RequestIDFromContext(ctx)
	projected, err := jobs.PublicJobResponseFromJob(job, string(requestID))
	if err != nil {
		return err
	}
	jobPath := "/api/v1/jobs/" + job.ID
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", jobPath)
	w.Header().Set("Retry-After", "1")
	writeIndexingJSON(w, http.StatusAccepted, jobStartResponse{
		JobID: job.ID,
		Job:   projected,
		Links: jobStartLinks{
			Job: jobPath,
		},
	})
	return nil
}

func writeIndexingServiceError(w http.ResponseWriter, ctx context.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		writeIndexingProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, auth.ErrEmailNotVerified):
		writeIndexingProblem(w, http.StatusForbidden, "Forbidden", "email address is not verified", "auth.email_unverified")
	case errors.Is(err, ErrNotFound):
		writeIndexingProblem(w, http.StatusNotFound, "Not Found", "indexing resource not found", "not_found")
	case errors.Is(err, ErrForbidden):
		writeIndexingProblem(w, http.StatusForbidden, "Forbidden", "indexing access denied", "forbidden")
	case errors.Is(err, ErrValidation):
		writeIndexingProblem(w, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	case errors.Is(err, ErrConflict):
		writeIndexingProblem(w, http.StatusConflict, "Conflict", "request conflicts with current indexing state", "conflict")
	default:
		observability.ReportUnexpectedError(ctx, err)
		writeIndexingProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

func writeIndexingJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeIndexingProblem(w http.ResponseWriter, status int, title, detail, code string) {
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(w, "", status, title, detail, code)
}

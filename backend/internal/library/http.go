package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/observability"

	"github.com/go-chi/chi/v5"
)

type ImportService interface {
	ImportConstraints(context.Context, Actor, string) (ImportConstraints, error)
	AuthorizeImportMutation(context.Context, Actor, string) error
	CreateImportJob(context.Context, Actor, string, CreateImportJobInput) (jobs.Job, error)
}

type ImportQueueService interface {
	CreateImportQueue(context.Context, CreateImportQueueInput) (ImportQueue, error)
	ListImportQueues(context.Context, ListImportQueuesInput) ([]ImportQueue, error)
	GetImportQueue(context.Context, GetImportQueueInput) (ImportQueue, error)
	UploadImportQueueItem(context.Context, UploadImportQueueItemInput) (ImportQueueItem, error)
	SkipImportQueueItem(context.Context, SkipImportQueueItemInput) (ImportQueue, error)
	StartImportQueue(context.Context, StartImportQueueInput) (ImportQueue, error)
	CancelImportQueue(context.Context, CancelImportQueueInput) (ImportQueue, error)
}

type HandlerService interface {
	ImportService
	ImportQueueService
	CreateBook(context.Context, Actor, string, BookInput) (Book, error)
	ListBooks(context.Context, Actor, string, Page) (BookPage, error)
	GetBook(context.Context, Actor, string) (Book, error)
	UpdateBook(context.Context, Actor, string, UpdateBookInput) (Book, error)
	DeleteBook(context.Context, Actor, string) error
	RestoreBook(context.Context, Actor, string) (Book, error)
	ListChapters(context.Context, Actor, string, Page) (ChapterPage, error)
	GetChapter(context.Context, Actor, string, ParagraphVariant) (ChapterContent, error)
	DeleteChapter(context.Context, Actor, string) error
	RestoreChapter(context.Context, Actor, string) (Chapter, error)
}

type CurrentUserService interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}

type Handlers struct {
	service        HandlerService
	currentUser    CurrentUserService
	allowedOrigins []string
}

type actorUserIDContextKey struct{}
type sessionWriteProofContextKey struct{}

func NewHandlers(service HandlerService, currentUser CurrentUserService, allowedOrigins []string) Handlers {
	if service == nil {
		panic("library: HandlerService is required")
	}
	if currentUser == nil {
		panic("library: CurrentUserService is required")
	}
	return Handlers{service: service, currentUser: currentUser, allowedOrigins: slices.Clone(allowedOrigins)}
}

func (h Handlers) Mount(router chi.Router) {
	router.Get("/projects/{projectId}/books", h.ListBooks)
	router.Get("/books/{bookId}", h.GetBook)
	router.Get("/books/{bookId}/chapters", h.ListChapters)
	router.Get("/chapters/{chapterId}", h.GetChapter)
	router.Get("/projects/{projectId}/import-constraints", h.GetImportConstraints)
	router.Get("/projects/{projectId}/import-queues", h.ListImportQueues)
	router.Get("/projects/{projectId}/import-queues/{queueId}", h.GetImportQueue)
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(h.allowedOrigins...))
		router.Use(h.CSRFMiddleware)
		router.Post("/projects/{projectId}/books", h.CreateBook)
		router.Patch("/books/{bookId}", h.UpdateBook)
		router.Delete("/books/{bookId}", h.DeleteBook)
		router.Post("/books/{bookId}/restore", h.RestoreBook)
		router.Delete("/chapters/{chapterId}", h.DeleteChapter)
		router.Post("/chapters/{chapterId}/restore", h.RestoreChapter)
		router.Post("/projects/{projectId}/imports", h.ImportBookFile)
		router.Post("/projects/{projectId}/import-queues", h.CreateImportQueue)
		router.Post("/projects/{projectId}/import-queues/{queueId}/items", h.UploadImportQueueItem)
		router.Delete("/projects/{projectId}/import-queues/{queueId}/items/{itemId}", h.SkipImportQueueItem)
		router.Post("/projects/{projectId}/import-queues/{queueId}/start", h.StartImportQueue)
		router.Delete("/projects/{projectId}/import-queues/{queueId}", h.CancelImportQueue)
	})
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
					writeImportProblem(w, http.StatusForbidden, "Forbidden", "missing CSRF token", "csrf.required")
				} else {
					writeImportProblem(w, http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
				}
				return
			}
			writeImportServiceError(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), actorUserIDContextKey{}, user.ID)
		ctx = context.WithValue(ctx, sessionWriteProofContextKey{}, proof)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h Handlers) sessionWriteProof(r *http.Request) auth.SessionWriteProof {
	proof, _ := r.Context().Value(sessionWriteProofContextKey{}).(auth.SessionWriteProof)
	return proof
}

func (h Handlers) actorUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if actorUserID, ok := r.Context().Value(actorUserIDContextKey{}).(string); ok {
		return actorUserID, true
	}
	user, err := h.currentUser.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if err != nil {
		writeImportServiceError(w, r, err)
		return "", false
	}
	return user.ID, true
}

type jobStartResponse struct {
	JobID string                 `json:"jobId"`
	Job   jobs.PublicJobResponse `json:"job"`
	Links jobStartLinks          `json:"links"`
}

type pageMeta struct {
	NextCursor *string `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
}

type jobStartLinks struct {
	Job string `json:"job"`
}

func writeJobStartResponse(w http.ResponseWriter, ctx context.Context, status int, job jobs.Job) error {
	requestID, _ := observability.RequestIDFromContext(ctx)
	projected, err := jobs.PublicJobResponseFromJob(job, string(requestID))
	if err != nil {
		return err
	}
	jobPath := "/api/v1/jobs/" + job.ID
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", jobPath)
	w.Header().Set("Retry-After", "1")
	writeImportJSON(w, status, jobStartResponse{
		JobID: job.ID,
		Job:   projected,
		Links: jobStartLinks{
			Job: jobPath,
		},
	})
	return nil
}

func writeImportServiceError(w http.ResponseWriter, r *http.Request, err error) {
	if httpapi.WriteQuotaProblem(w, err, time.Now()) {
		return
	}
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeImportProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, auth.ErrEmailNotVerified):
		writeImportProblem(w, http.StatusForbidden, "Forbidden", "email address is not verified", "auth.email_unverified")
	case errors.Is(err, ErrNotFound):
		writeImportProblem(w, http.StatusNotFound, "Not Found", "project not found", "not_found")
	case errors.Is(err, ErrForbidden):
		writeImportProblem(w, http.StatusForbidden, "Forbidden", "project access denied", "forbidden")
	case errors.Is(err, ErrValidation):
		writeImportProblem(w, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	case errors.Is(err, ErrImportSequenceConflict):
		writeImportProblem(w, http.StatusConflict, "Conflict", "existing book order is not contiguous", "import_sequence_conflict")
	case errors.Is(err, ErrImportExpectedOrderConflict):
		writeImportProblem(w, http.StatusConflict, "Conflict", "expected book order is stale", "import_expected_order_conflict")
	case errors.Is(err, ErrConflict):
		writeImportProblem(w, http.StatusConflict, "Conflict", "request conflicts with current import state", "conflict")
	case errors.Is(err, ErrImportFileTooLarge):
		writeImportProblem(w, http.StatusRequestEntityTooLarge, "Payload Too Large", "import file is too large", "import.file_too_large")
	case errors.Is(err, ErrImportUnsupportedMediaType):
		writeImportProblem(w, http.StatusUnsupportedMediaType, "Unsupported Media Type", "import file type is not supported", "import.unsupported_media_type")
	case errors.Is(err, ErrImportContentMismatch):
		writeImportProblem(w, http.StatusUnprocessableEntity, "Unprocessable Entity", "import file content does not match its declared type", "import.content_mismatch")
	case errors.Is(err, ErrImportNoContent):
		writeImportProblem(w, http.StatusUnprocessableEntity, "Unprocessable Entity", "import file has no content", "import.no_content")
	default:
		observability.ReportUnexpectedError(r.Context(), err)
		writeImportProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

func pageFromRequest(r *http.Request) (Page, error) {
	values := r.URL.Query()
	page := Page{Cursor: values.Get("cursor")}
	if rawLimit := strings.TrimSpace(values.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return Page{}, ErrValidation
		}
		page.Limit = limit
	}
	return page, nil
}

const maxImportJSONBytes = 1 << 20

func decodeImportJSON(r *http.Request, target any) error {
	return decodeJSONWithLimit(r, target, maxImportJSONBytes)
}

func decodeJSONWithLimit(r *http.Request, target any, limit int64) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrValidation
	}
	if decoder.InputOffset() > limit {
		return ErrValidation
	}
	return nil
}
func writeImportJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeImportProblem(w http.ResponseWriter, status int, title, detail, code string) {
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(w, "", status, title, detail, code)
}

func formatImportTime(value time.Time) string {
	return value.Format("2006-01-02T15:04:05.000Z07:00")
}

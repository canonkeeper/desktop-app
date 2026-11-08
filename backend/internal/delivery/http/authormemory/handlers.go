// Package authormemory delivers the page «Память» over HTTP (specs/2026-11-01-writer-assistant-index.md,
// R4.5): listAuthorMemory for the members of a project; createAuthorMemory, updateAuthorMemory and
// deleteAuthorMemory for an editor or above, each behind the session's write proof, the first with an
// Idempotency-Key and the other two with the revision they saw.
package authormemory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/authormemory"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"
	service "canonkeeper/backend/internal/services/authormemory"

	"github.com/go-chi/chi/v5"
)

const maxBody = 64 << 10

type CurrentUser interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}

// UseCases are the operations of the page.
type UseCases struct {
	List   service.List
	Create service.Create
	Update service.Update
	Delete service.Delete
}

type Handlers struct {
	useCases       UseCases
	currentUser    CurrentUser
	allowedOrigins []string
}

func NewHandlers(useCases UseCases, currentUser CurrentUser, allowedOrigins []string) Handlers {
	if currentUser == nil {
		panic("author memory HTTP: dependencies are required")
	}
	return Handlers{useCases: useCases, currentUser: currentUser, allowedOrigins: slices.Clone(allowedOrigins)}
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(handlers.allowedOrigins...))
		router.Get("/projects/{projectId}/author-memory", handlers.list)
		router.Post("/projects/{projectId}/author-memory", handlers.create)
		router.Put("/projects/{projectId}/author-memory/{recordId}", handlers.update)
		router.Delete("/projects/{projectId}/author-memory/{recordId}", handlers.delete)
	})
}

func projectID(r *http.Request) string { return strings.TrimSpace(chi.URLParam(r, "projectId")) }

func (handlers Handlers) list(w http.ResponseWriter, r *http.Request) {
	user, err := handlers.currentUser.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if err != nil {
		writeError(w, r.Context(), err)
		return
	}
	query := map[string]string{}
	for name, list := range r.URL.Query() {
		if !slices.Contains([]string{"kind", "q", "jobId", "cursor", "limit"}, name) || len(list) != 1 {
			writeError(w, r.Context(), domain.ErrInvalid)
			return
		}
		query[name] = list[0]
	}
	input := service.ListInput{Kind: query["kind"], Query: query["q"], JobID: query["jobId"], Cursor: query["cursor"]}
	if value, present := query["limit"]; present {
		if input.Limit, err = strconv.Atoi(value); err != nil || input.Limit < 1 {
			writeError(w, r.Context(), domain.ErrInvalid)
			return
		}
	}
	page, err := handlers.useCases.List.Execute(r.Context(), service.Actor{UserID: user.ID, ProjectID: projectID(r)}, input)
	if err != nil {
		writeError(w, r.Context(), err)
		return
	}
	writeJSON(w, http.StatusOK, pageDTO(page))
}

// writer checks the write proof of the session; the role is checked by the use case and, in the
// transaction of the write, by the store.
func (handlers Handlers) writer(w http.ResponseWriter, r *http.Request) (service.Write, bool) {
	user, proof, err := handlers.currentUser.AuthenticateWrite(r.Context(), auth.SessionCookieValue(r), r.Header.Get("X-CSRF-Token"))
	if err != nil {
		writeError(w, r.Context(), err)
		return service.Write{}, false
	}
	return service.Write{Actor: service.Actor{UserID: user.ID, ProjectID: projectID(r)}, Proof: proof}, true
}

// recordBody is what the page sends for a record: an absent chapter or name is none.
type recordBody struct {
	Kind             string `json:"kind"`
	Text             string `json:"text"`
	Chapter          string `json:"chapter"`
	Name             string `json:"name"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

func (body recordBody) input() service.Input {
	return service.Input{Kind: body.Kind, Text: body.Text, Chapter: body.Chapter, Name: body.Name}
}

func readBody(w http.ResponseWriter, r *http.Request, body *recordBody) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil || !utf8.Valid(raw) {
		return domain.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(body) != nil {
		return domain.ErrInvalid
	}
	return nil
}

func (handlers Handlers) create(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	var body recordBody
	if err := readBody(w, r, &body); err != nil || body.ExpectedRevision != 0 {
		writeError(w, r.Context(), domain.ErrInvalid)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		writeError(w, r.Context(), domain.ErrInvalid)
		return
	}
	view, err := handlers.useCases.Create.Execute(r.Context(), actor, body.input(), service.RequestPath{Key: keys[0], Method: r.Method, Path: r.URL.Path})
	if err != nil {
		writeError(w, r.Context(), err)
		return
	}
	writeJSON(w, http.StatusCreated, recordDTO(view))
}

func (handlers Handlers) update(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	var body recordBody
	if err := readBody(w, r, &body); err != nil {
		writeError(w, r.Context(), err)
		return
	}
	view, err := handlers.useCases.Update.Execute(r.Context(), actor, chi.URLParam(r, "recordId"), body.ExpectedRevision, body.input())
	if err != nil {
		writeError(w, r.Context(), err)
		return
	}
	writeJSON(w, http.StatusOK, recordDTO(view))
}

func (handlers Handlers) delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()["expectedRevision"]
	if len(values) != 1 || len(r.URL.Query()) != 1 {
		writeError(w, r.Context(), domain.ErrInvalid)
		return
	}
	revision, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		writeError(w, r.Context(), domain.ErrInvalid)
		return
	}
	if err := handlers.useCases.Delete.Execute(r.Context(), actor, chi.URLParam(r, "recordId"), revision); err != nil {
		writeError(w, r.Context(), err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// The JSON shapes of contracts/openapi.json (AuthorMemory* schemas).

type chapterRef struct {
	Key       string `json:"key"`
	ChapterID string `json:"chapterId"`
	BookID    string `json:"bookId"`
	BookOrder int    `json:"bookOrder"`
	BookTitle string `json:"bookTitle"`
	Title     string `json:"title"`
	Number    int    `json:"number,omitempty"`
}

type record struct {
	ID        string      `json:"id"`
	Kind      string      `json:"kind"`
	Text      string      `json:"text"`
	Chapter   *chapterRef `json:"chapter,omitempty"`
	Name      string      `json:"name,omitempty"`
	Revision  int64       `json:"revision"`
	Author    string      `json:"author"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
}

type pageMeta struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
	TotalCount int64   `json:"totalCount"`
}

type page struct {
	Data []record `json:"data"`
	Meta pageMeta `json:"meta"`
}

func chapterDTO(chapter *sagamemory.Chapter) *chapterRef {
	if chapter == nil {
		return nil
	}
	return &chapterRef{
		Key: chapter.Key, ChapterID: chapter.ID, BookID: chapter.BookID, BookOrder: chapter.BookOrder,
		BookTitle: chapter.BookTitle, Title: chapter.Title, Number: chapter.Number,
	}
}

func recordDTO(view service.View) record {
	return record{
		ID: view.ID, Kind: string(view.Kind), Text: view.Text, Chapter: chapterDTO(view.Chapter), Name: view.Name,
		Revision: view.Revision, Author: view.AuthorName, CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt,
	}
}

func pageDTO(view service.PageView) page {
	result := page{Data: make([]record, len(view.Records)), Meta: pageMeta{TotalCount: view.Total}}
	for index, item := range view.Records {
		result.Data[index] = recordDTO(item)
	}
	if view.NextCursor != "" {
		next := view.NextCursor
		result.Meta.HasMore, result.Meta.NextCursor = true, &next
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, ctx context.Context, err error) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, auth.ErrInvalidCSRF):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
	case errors.Is(err, domain.ErrForbidden):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "writing the memory needs an editor", "forbidden")
	case errors.Is(err, domain.ErrNotFound):
		httpapi.WriteProblem(w, "", http.StatusNotFound, "Not Found", "record not found", "not_found")
	case errors.Is(err, domain.ErrConflict):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the record changed", "conflict")
	case errors.Is(err, domain.ErrNoChapter):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the saga has no such chapter", "author_memory.no_chapter")
	case errors.Is(err, domain.ErrInvalid):
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	default:
		observability.ReportUnexpectedError(ctx, err)
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

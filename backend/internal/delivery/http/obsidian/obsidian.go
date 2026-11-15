// SPDX-License-Identifier: AGPL-3.0-only

// Package obsidian serves the bridge of the desktop app with a vault of Obsidian (specs/2026-11-13-desk-obsidian.md):
// getObsidianVault, beginObsidianVaultChoice, unlinkObsidianVault, scanObsidianVault and previewObsidianImport. Only the
// desktop mounts it. No operation takes an absolute path, and none gives one but the path of the vault in
// getObsidianVault (OB0.3).
package obsidian

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"
	service "canonkeeper/backend/internal/services/obsidian"

	"github.com/go-chi/chi/v5"
)

// CurrentUserService names the author of a request by his session.
type CurrentUserService interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}

// The use cases of the bridge.
type (
	ReadUseCase interface {
		Execute(ctx context.Context, actorUserID, projectID string) (service.VaultState, error)
	}
	BeginUseCase interface {
		Execute(ctx context.Context, actorUserID, projectID string) (string, error)
	}
	UnlinkUseCase interface {
		Execute(ctx context.Context, actorUserID, projectID string) error
	}
	ScanUseCase interface {
		Execute(ctx context.Context, input service.ScanInput) (service.Scanned, error)
	}
	PreviewUseCase interface {
		Execute(ctx context.Context, input service.PreviewInput) (service.Preview, error)
	}
	LoadUseCase interface {
		Execute(ctx context.Context, input service.LoadInput) (service.Loaded, error)
	}
	UpdateUseCase interface {
		Execute(ctx context.Context, input service.UpdateInput) (service.Updated, error)
	}
	BookFolderUseCase interface {
		Execute(ctx context.Context, input service.BookFolderInput) error
	}
	MaterialsUseCase interface {
		Execute(ctx context.Context, input service.MaterialsInput) (service.MaterialsLoaded, error)
	}
)

// Handlers are the operations of the bridge.
type Handlers struct {
	Read           ReadUseCase
	Begin          BeginUseCase
	Unlink         UnlinkUseCase
	Scan           ScanUseCase
	Preview        PreviewUseCase
	Load           LoadUseCase
	Update         UpdateUseCase
	LinkBook       BookFolderUseCase
	UnlinkBook     BookFolderUseCase
	Materials      MaterialsUseCase
	Users          CurrentUserService
	AllowedOrigins []string
}

// Mount serves the operations: a read under the session of the window; a change under its Origin, its session and its
// request-forgery token.
func (handlers Handlers) Mount(router chi.Router) {
	if handlers.Read == nil || handlers.Begin == nil || handlers.Unlink == nil || handlers.Scan == nil || handlers.Preview == nil ||
		handlers.Load == nil || handlers.Update == nil || handlers.LinkBook == nil || handlers.UnlinkBook == nil || handlers.Materials == nil ||
		handlers.Users == nil {
		panic("obsidian HTTP: dependencies are required")
	}
	router.Get("/projects/{projectId}/obsidian", handlers.read)
	router.Get("/projects/{projectId}/obsidian/tree", handlers.scan)
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(handlers.AllowedOrigins...))
		router.Post("/projects/{projectId}/obsidian/choice", handlers.begin)
		router.Delete("/projects/{projectId}/obsidian", handlers.unlink)
		router.Post("/projects/{projectId}/obsidian/preview", handlers.preview)
		router.Post("/projects/{projectId}/obsidian/books", handlers.load)
		router.Post("/projects/{projectId}/obsidian/books/{bookId}/update", handlers.update)
		router.Put("/projects/{projectId}/obsidian/books/{bookId}", handlers.linkBook)
		router.Delete("/projects/{projectId}/obsidian/books/{bookId}", handlers.unlinkBook)
		router.Post("/projects/{projectId}/obsidian/materials", handlers.materials)
	})
}

func (handlers Handlers) read(response http.ResponseWriter, request *http.Request) {
	user, ok := handlers.reader(response, request)
	if !ok {
		return
	}
	state, err := handlers.Read.Execute(request.Context(), user.ID, chi.URLParam(request, "projectId"))
	if err != nil {
		writeError(response, err)
		return
	}
	body := vaultBody{Linked: state.Linked, Choosing: state.Choosing, Refused: string(state.Refused)}
	if state.Linked {
		body.Path, body.Name, body.Availability = state.Path, state.Name, "unknown"
		if state.Available != nil {
			body.Availability = map[bool]string{true: "available", false: "missing"}[*state.Available]
		}
		// specs/2026-11-14-desk-obsidian-books.md OK2.6: the linked books and the books to link anew.
		body.Books, body.Relink = vaultBooksOf(state.Books), vaultBooksOf(state.Relink)
	}
	writeJSON(response, http.StatusOK, body)
}

func (handlers Handlers) begin(response http.ResponseWriter, request *http.Request) {
	user, ok := handlers.writer(response, request)
	if !ok {
		return
	}
	number, err := handlers.Begin.Execute(request.Context(), user.ID, chi.URLParam(request, "projectId"))
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusAccepted, choiceBody{Request: number})
}

func (handlers Handlers) unlink(response http.ResponseWriter, request *http.Request) {
	user, ok := handlers.writer(response, request)
	if !ok {
		return
	}
	if err := handlers.Unlink.Execute(request.Context(), user.ID, chi.URLParam(request, "projectId")); err != nil {
		writeError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handlers Handlers) scan(response http.ResponseWriter, request *http.Request) {
	user, ok := handlers.reader(response, request)
	if !ok {
		return
	}
	scanned, err := handlers.Scan.Execute(request.Context(), service.ScanInput{ActorUserID: user.ID, ProjectID: chi.URLParam(request, "projectId")})
	if err != nil {
		writeError(response, err)
		return
	}
	body := treeBody{Folders: []folderBody{}, Truncated: scanned.Truncated, Entries: scanned.Entries, Skipped: skippedBody(scanned.Skipped)}
	for _, folder := range scanned.Folders {
		item := folderBody{
			Path: folder.Path, Name: folder.Name, Depth: folder.Depth, Notes: folder.Notes, Below: folder.Below,
			Skipped: skippedBody(folder.Skipped),
		}
		if folder.Hint != nil {
			item.Hint = &hintBody{Kind: string(folder.Hint.Kind), Material: string(folder.Hint.Material)}
		}
		// specs/2026-11-14-desk-obsidian-books.md OK0.4: a folder whose volume is in work has no book yet; the preview says it.
		if folder.Book != nil && !folder.Book.Queued {
			item.Book = &bookBody{BookID: folder.Book.BookID, Title: folder.Book.BookTitle}
		}
		body.Folders = append(body.Folders, item)
	}
	writeJSON(response, http.StatusOK, body)
}

func (handlers Handlers) reader(response http.ResponseWriter, request *http.Request) (auth.User, bool) {
	if request.URL.RawQuery != "" {
		writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation", nil)
		return auth.User{}, false
	}
	user, err := handlers.Users.CurrentUser(request.Context(), auth.SessionCookieValue(request))
	if err != nil {
		writeAuthError(response, err)
		return auth.User{}, false
	}
	return user, true
}

func (handlers Handlers) writer(response http.ResponseWriter, request *http.Request) (auth.User, bool) {
	if request.URL.RawQuery != "" {
		writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation", nil)
		return auth.User{}, false
	}
	user, _, err := handlers.Users.AuthenticateWrite(request.Context(), auth.SessionCookieValue(request), request.Header.Get("X-CSRF-Token"))
	if err != nil {
		writeAuthError(response, err)
		return auth.User{}, false
	}
	return user, true
}

type vaultBody struct {
	Linked       bool   `json:"linked"`
	Path         string `json:"path,omitempty"`
	Name         string `json:"name,omitempty"`
	Availability string `json:"availability,omitempty"`
	Choosing     bool   `json:"choosing"`
	Refused      string `json:"refused,omitempty"`
	// Books and Relink are the books of OK2.6, only for a linked saga.
	Books  []vaultBookBody `json:"books,omitempty"`
	Relink []vaultBookBody `json:"relink,omitempty"`
}

type vaultBookBody struct {
	BookID   string     `json:"bookId"`
	Title    string     `json:"title"`
	Folder   string     `json:"folder"`
	LoadedAt *time.Time `json:"loadedAt"`
	// Presence says whether the folder is in the vault now: present, missing or unknown.
	Presence string `json:"presence"`
	// Excluded and Nested are the composition the link keeps (specs/2026-11-15-desk-obsidian-web.md OW2.5): paths in the vault.
	Excluded []string `json:"excluded"`
	Nested   []string `json:"nested"`
}

func vaultBooksOf(books []service.VaultBook) []vaultBookBody {
	body := []vaultBookBody{}
	for _, book := range books {
		item := vaultBookBody{
			BookID: book.BookID, Title: book.Title, Folder: book.Folder, Presence: "unknown",
			Excluded: append([]string{}, book.Choice.Excluded...), Nested: append([]string{}, book.Choice.Nested...),
		}
		if book.LoadedAt != nil {
			at := book.LoadedAt.UTC()
			item.LoadedAt = &at
		}
		if book.Present != nil {
			item.Presence = map[bool]string{true: "present", false: "missing"}[*book.Present]
		}
		body = append(body, item)
	}
	return body
}

type choiceBody struct {
	Request string `json:"request"`
}

type treeBody struct {
	Folders   []folderBody `json:"folders"`
	Truncated bool         `json:"truncated"`
	Entries   int          `json:"entries"`
	Skipped   []skipBody   `json:"skipped"`
}

type folderBody struct {
	Path    string     `json:"path"`
	Name    string     `json:"name"`
	Depth   int        `json:"depth"`
	Notes   int        `json:"notes"`
	Below   int        `json:"below"`
	Skipped []skipBody `json:"skipped"`
	Hint    *hintBody  `json:"hint,omitempty"`
	Book    *bookBody  `json:"book,omitempty"`
}

type hintBody struct {
	Kind     string `json:"kind"`
	Material string `json:"material,omitempty"`
}

type bookBody struct {
	BookID string `json:"bookId"`
	Title  string `json:"title"`
}

type skipBody struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// skippedBody is a count by its reasons in the order of the reasons.
func skippedBody(skipped domain.Skipped) []skipBody {
	body := []skipBody{}
	for reason, count := range skipped {
		if count > 0 {
			body = append(body, skipBody{Reason: string(reason), Count: count})
		}
	}
	slices.SortFunc(body, func(left, right skipBody) int {
		if left.Reason < right.Reason {
			return -1
		}
		if left.Reason > right.Reason {
			return 1
		}
		return 0
	})
	return body
}

// problemBody is a refusal of the bridge with what the page needs to say it: the reason of a vault that does not open,
// the numbers of a choice too large, the folders that overlap.
type problemBody struct {
	httpapi.Problem
	VaultReason string `json:"vaultReason,omitempty"`
	Notes       int64  `json:"notes,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`
	LimitNotes  int64  `json:"limitNotes,omitempty"`
	LimitBytes  int64  `json:"limitBytes,omitempty"`
	// Entries and LimitEntries are a walk of the chosen folders that stopped past the entries a look walks.
	Entries      int64    `json:"entries,omitempty"`
	LimitEntries int64    `json:"limitEntries,omitempty"`
	Folders      []string `json:"folders,omitempty"`
	// Queue is the live queue of the saga a write of part A2 waits for (obsidian.queue_busy).
	Queue *queueBody `json:"queue,omitempty"`
	// Unread are the notes of the folder that did not read now by their reasons (obsidian.notes_unread).
	Unread []skipBody `json:"unread,omitempty"`
	// BookID is the book that holds the folder of a request (obsidian.folder_linked).
	BookID string `json:"bookId,omitempty"`
	// Records, SagaRecords and LimitRecords are a load of notes past the records of a load or of a saga after it
	// (obsidian.too_large, specs/2026-11-15-desk-obsidian-notes.md ON1.7).
	Records      int `json:"records,omitempty"`
	SagaRecords  int `json:"sagaRecords,omitempty"`
	LimitRecords int `json:"limitRecords,omitempty"`
}

func writeError(response http.ResponseWriter, err error) {
	var busy service.Busy
	var missing service.VaultMissing
	var overlap domain.Overlap
	var tooLarge domain.TooLarge
	var queueBusy service.QueueBusy
	var changed service.FolderChanged
	var failed service.LoadFailed
	var folderMissing service.FolderMissing
	var folderLinked service.FolderLinked
	var unread service.NotesUnread
	var records domain.TooManyRecords
	switch {
	// specs/2026-11-14-desk-obsidian-books.md, Data: the refusals of the writes of part A2.
	case errors.As(err, &queueBusy):
		writeProblem(response, http.StatusConflict, "Conflict", "the saga has a live queue of the volumes", domain.CodeQueueBusy, func(body *problemBody) {
			body.Queue = &queueBody{ID: queueBusy.Queue.ID, Status: queueBusy.Queue.Status}
		})
	case errors.As(err, &changed):
		writeProblem(response, http.StatusConflict, "Conflict", "a folder changed since its preview", domain.CodeFolderChanged, func(body *problemBody) {
			body.Folders = changed.Folders
		})
	case errors.As(err, &failed):
		writeProblem(response, failureStatus(failed.Code), "Load Failed", "the load failed after its queue was made", failed.Code, nil)
	case errors.Is(err, service.ErrLoadInterrupted):
		writeProblem(response, http.StatusConflict, "Conflict", "the load of the key was interrupted", domain.CodeLoadInterrupted, nil)
	case errors.Is(err, service.ErrReplayConflict):
		writeProblem(response, http.StatusConflict, "Conflict", "the key was used for another request", "idempotency.conflict", nil)
	case errors.Is(err, service.ErrFingerprint):
		writeProblem(response, http.StatusBadRequest, "Bad Request", "a folder of a book has no fingerprint", "request.validation", nil)
	case errors.Is(err, service.ErrConflict):
		writeProblem(response, http.StatusConflict, "Conflict", "the write conflicts with the state of the saga", "conflict", nil)
	case errors.As(err, &folderMissing):
		writeProblem(response, http.StatusConflict, "Conflict", "the folder is not in the vault", domain.CodeFolderMissing, func(body *problemBody) {
			body.Folders = []string{folderMissing.Folder}
		})
	case errors.As(err, &folderLinked):
		writeProblem(response, http.StatusConflict, "Conflict", "another book holds the folder", domain.CodeFolderLinked, func(body *problemBody) {
			body.Folders, body.BookID = []string{folderLinked.Folder}, folderLinked.BookID
		})
	case errors.As(err, &unread):
		writeProblem(response, http.StatusConflict, "Conflict", "notes of the folder did not read", domain.CodeNotesUnread, func(body *problemBody) {
			body.Unread = skippedBody(unread.Unread)
		})
	case errors.Is(err, service.ErrBookNotLinked):
		writeProblem(response, http.StatusConflict, "Conflict", "the book is not linked with a folder", domain.CodeBookNotLinked, nil)
	case errors.Is(err, service.ErrNothingToApply):
		writeProblem(response, http.StatusConflict, "Conflict", "the update changes nothing", domain.CodeNothingToApply, nil)
	case errors.As(err, &busy):
		response.Header().Set("Retry-After", retryAfter(busy.RetryAfter))
		writeProblem(response, http.StatusTooManyRequests, "Too Many Requests", "another operation reads a vault", domain.CodeBusy, nil)
	case errors.Is(err, service.ErrSlow):
		writeProblem(response, http.StatusServiceUnavailable, "Service Unavailable", "the vault did not answer in time", domain.CodeSlow, nil)
	case errors.As(err, &missing):
		writeProblem(response, http.StatusConflict, "Conflict", "the vault does not open", domain.CodeVaultMissing, func(body *problemBody) {
			body.VaultReason = string(missing.Reason)
		})
	case errors.As(err, &overlap):
		writeProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "the folders overlap", domain.CodeFoldersOverlap, func(body *problemBody) {
			body.Folders = []string{overlap.Outer, overlap.Inner}
		})
	case errors.As(err, &records):
		writeProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "too many records", domain.CodeTooLarge, func(body *problemBody) {
			body.Records, body.SagaRecords, body.LimitRecords = records.Records, records.Saga, domain.MaxRecords
		})
	case errors.As(err, &tooLarge):
		writeProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "the chosen folders are too large", domain.CodeTooLarge, func(body *problemBody) {
			body.Notes, body.Bytes, body.LimitNotes, body.LimitBytes = tooLarge.Notes, tooLarge.Bytes, domain.MaxNotes, domain.MaxSelectedBytes
			if tooLarge.Entries > domain.MaxEntries {
				body.Entries, body.LimitEntries = tooLarge.Entries, domain.MaxEntries
			}
		})
	case errors.Is(err, domain.ErrPath):
		writeProblem(response, http.StatusBadRequest, "Bad Request", "a folder is not a folder of the vault", "request.validation", nil)
	case errors.Is(err, service.ErrNotLinked):
		writeProblem(response, http.StatusConflict, "Conflict", "the saga is not linked with a vault", domain.CodeNotLinked, nil)
	case errors.Is(err, service.ErrChoiceUnavailable):
		writeProblem(response, http.StatusServiceUnavailable, "Service Unavailable", "no window chooses a folder", domain.CodeChoiceUnavailable, nil)
	case errors.Is(err, service.ErrNotFound):
		writeProblem(response, http.StatusNotFound, "Not Found", "saga not found", "not_found", nil)
	case errors.Is(err, service.ErrForbidden):
		writeProblem(response, http.StatusForbidden, "Forbidden", "the role does not change the books of the saga", "forbidden", nil)
	case errors.Is(err, context.Canceled):
		writeProblem(response, http.StatusServiceUnavailable, "Service Unavailable", "the request went away", "request.canceled", nil)
	default:
		writeProblem(response, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error", nil)
	}
}

func writeAuthError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		writeProblem(response, http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required", nil)
	case errors.Is(err, auth.ErrInvalidCSRF):
		writeProblem(response, http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid", nil)
	default:
		writeProblem(response, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error", nil)
	}
}

func writeProblem(response http.ResponseWriter, status int, title, detail, code string, fill func(*problemBody)) {
	body := problemBody{Problem: httpapi.Problem{
		Type: "about:blank", Title: title, Status: status, Detail: detail, Code: code,
		RequestID: response.Header().Get(observability.RequestIDHeader),
	}}
	if fill != nil {
		fill(&body)
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/problem+json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

// retryAfter is a time in whole seconds, at least one.
func retryAfter(duration time.Duration) string {
	return strconv.FormatInt(max(int64((duration+time.Second-1)/time.Second), 1), 10)
}

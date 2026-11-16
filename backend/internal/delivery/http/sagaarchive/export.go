// SPDX-License-Identifier: AGPL-3.0-only

// Package sagaarchive serves the saga file (specs/2026-11-13-desk-sagafile.md): getSagaArchive saves a saga into it,
// openSagaArchive opens one as a new saga, listSagaTranscripts and getSagaTranscript read the chats it brought.
package sagaarchive

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/bookexport"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/sagaarchive"

	"github.com/go-chi/chi/v5"
)

// CurrentUserService names the author of a request by his session.
type CurrentUserService interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}

// ExportUseCase saves a saga into its file.
type ExportUseCase interface {
	Prepare(context.Context, service.ExportInput) (*service.Prepared, error)
}

// MountExport serves getSagaArchive (SF2.1): a GET under the session, the rights and the saga checked before the first byte;
// the file goes as a stream that a failure breaks rather than ends (SF2.3) and that lasts no longer than SaveDeadline.
func MountExport(router chi.Router, export ExportUseCase, currentUser CurrentUserService) {
	if export == nil || currentUser == nil {
		panic("saga archive HTTP: dependencies are required")
	}
	router.Get("/projects/{projectId}/saga-archive", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		user, ok := currentAuthor(response, request, currentUser)
		if !ok {
			return
		}
		if request.URL.RawQuery != "" {
			writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), service.SaveDeadline)
		defer cancel()
		prepared, err := export.Prepare(ctx, service.ExportInput{ActorUserID: user.ID, ProjectID: chi.URLParam(request, "projectId")})
		var busy service.Busy
		switch {
		case errors.As(err, &busy):
			httpapi.WriteRateLimited(response, "", busy.RetryAfter)
			return
		case errors.Is(err, service.ErrNotFound):
			writeProblem(response, http.StatusNotFound, "Not Found", "saga not found", "not_found")
			return
		case errors.Is(err, service.ErrForbidden):
			writeProblem(response, http.StatusForbidden, "Forbidden", "only the owner saves a saga into a file", "forbidden")
			return
		case err != nil:
			logFailure("prepare", chi.URLParam(request, "projectId"), err)
			writeProblem(response, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
			return
		}
		defer prepared.Close()
		WriteArchive(ctx, response, prepared, chi.URLParam(request, "projectId"))
	})
}

// WriteArchive answers with a saga file prepared already (SF2.3; specs/2026-11-16-desk-site-saga.md TR1.2): the one answer
// of getSagaArchive and of the transfer of a saga to the app — application/zip named by the title of the saga, no longer than
// SaveDeadline, and a failure after the first byte breaks the connection rather than ends the file, with the line
// saga_archive_failed in the journal. The caller closes what is prepared.
func WriteArchive(ctx context.Context, response http.ResponseWriter, prepared *service.Prepared, projectID string) {
	_ = http.NewResponseController(response).SetWriteDeadline(time.Now().Add(service.SaveDeadline))
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/zip")
	// The standard library names a non-ASCII file as RFC 6266 wants: filename*=utf-8''…
	response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": bookexport.FileName(prepared.Name, bookexport.Format(domain.Extension)),
	}))
	response.WriteHeader(http.StatusOK)
	if err := prepared.WriteTo(ctx, response); err != nil {
		logFailure("write", projectID, err)
		// The status has gone. Only a broken connection tells the reader that the file is not whole.
		panic(http.ErrAbortHandler)
	}
}

// logFailure tells the journal why a saga did not go into its file: the stage and the error, whose words name records of the
// file and steps of the server, never the texts of the saga (a refusal of the writer names the record, not its value).
func logFailure(stage, projectID string, err error) {
	message := err.Error()
	if len(message) > 400 {
		message = message[:400]
	}
	line, _ := json.Marshal(map[string]string{"event": "saga_archive_failed", "stage": stage, "projectId": projectID, "error": message})
	log.Print(string(line))
}

func currentAuthor(response http.ResponseWriter, request *http.Request, currentUser CurrentUserService) (auth.User, bool) {
	user, err := currentUser.CurrentUser(request.Context(), auth.SessionCookieValue(request))
	if err != nil {
		writeAuthError(response, err)
		return auth.User{}, false
	}
	return user, true
}

func writeAuthError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		writeProblem(response, http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, auth.ErrInvalidCSRF):
		writeProblem(response, http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
	default:
		writeProblem(response, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

func writeProblem(response http.ResponseWriter, status int, title, detail, code string) {
	response.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(response, "", status, title, detail, code)
}

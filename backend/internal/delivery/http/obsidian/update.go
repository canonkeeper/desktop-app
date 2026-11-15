// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"canonkeeper/backend/internal/library"
	service "canonkeeper/backend/internal/services/obsidian"

	"github.com/go-chi/chi/v5"
)

// The modes of updateBookFromObsidian.
const (
	updatePreview = "preview"
	updateApply   = "apply"
)

type updateRequest struct {
	Mode                string `json:"mode"`
	ExpectedFingerprint string `json:"expectedFingerprint"`
	ConfirmUnread       bool   `json:"confirmUnread"`
}

// update is updateBookFromObsidian (specs/2026-11-14-desk-obsidian-books.md OK2.1, OK2.2, OK2.5): its mode, the fingerprint
// of the preview an application applies and the word of the author on the notes that did not read, one Idempotency-Key. The
// answer is the start of the import Job as the import of a book gives it; a preview gives the fingerprint of the glue and
// the report of the reading of the folder beside it.
func (handlers Handlers) update(response http.ResponseWriter, request *http.Request) {
	user, proof, ok := handlers.provenWriter(response, request)
	if !ok {
		return
	}
	key, ok := idempotencyKey(response, request)
	if !ok {
		return
	}
	body, err := readUpdate(request.Body)
	if err != nil {
		writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation", nil)
		return
	}
	updated, err := handlers.Update.Execute(request.Context(), service.UpdateInput{
		ActorUserID: user.ID, ProjectID: chi.URLParam(request, "projectId"), BookID: chi.URLParam(request, "bookId"),
		Apply: body.Mode == updateApply, Expected: body.ExpectedFingerprint, ConfirmUnread: body.ConfirmUnread,
		Key: key, Method: request.Method, Path: request.URL.Path, Proof: proof,
	})
	var refused service.ImportRefused
	if errors.As(err, &refused) {
		library.WriteImportError(response, request, refused.Err)
		return
	}
	if err != nil {
		writeError(response, err)
		return
	}
	var beside func(library.JobStart) any
	if updated.Book != nil {
		report := bookPreviewBodyOf(*updated.Book)
		beside = func(start library.JobStart) any {
			return updatePreviewBody{JobStart: start, Fingerprint: updated.Fingerprint, Book: report}
		}
	}
	if err := library.WriteJobStart(response, request.Context(), http.StatusAccepted, updated.Job, beside); err != nil {
		writeError(response, err)
	}
}

// updatePreviewBody is the start of the Job of a preview with the fingerprint of the glue and the report of the folder.
type updatePreviewBody struct {
	library.JobStart
	Fingerprint string          `json:"fingerprint"`
	Book        bookPreviewBody `json:"book"`
}

func readUpdate(body io.Reader) (updateRequest, error) {
	decoder := json.NewDecoder(io.LimitReader(body, maxPreviewBody))
	decoder.DisallowUnknownFields()
	var input updateRequest
	if err := decoder.Decode(&input); err != nil || decoder.More() {
		return updateRequest{}, errPreviewRequest
	}
	switch {
	case input.Mode == updatePreview && input.ExpectedFingerprint == "" && !input.ConfirmUnread:
	case input.Mode == updateApply && isFingerprint(input.ExpectedFingerprint):
	default:
		return updateRequest{}, errPreviewRequest
	}
	return input, nil
}

type bookFolderRequest struct {
	Folder *string `json:"folder"`
}

// linkBook is linkBookToObsidianFolder (OK2.3): the folder in the body, never in the address (OB0.4).
func (handlers Handlers) linkBook(response http.ResponseWriter, request *http.Request) {
	user, ok := handlers.writer(response, request)
	if !ok {
		return
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxPreviewBody))
	decoder.DisallowUnknownFields()
	var body bookFolderRequest
	if err := decoder.Decode(&body); err != nil || decoder.More() || body.Folder == nil || len(*body.Folder) > maxRequestPathLen {
		writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation", nil)
		return
	}
	if err := handlers.LinkBook.Execute(request.Context(), service.BookFolderInput{
		ActorUserID: user.ID, ProjectID: chi.URLParam(request, "projectId"), BookID: chi.URLParam(request, "bookId"), Folder: *body.Folder,
	}); err != nil {
		writeError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

// unlinkBook is unlinkBookFromObsidianFolder (OK2.3).
func (handlers Handlers) unlinkBook(response http.ResponseWriter, request *http.Request) {
	user, ok := handlers.writer(response, request)
	if !ok {
		return
	}
	if err := handlers.UnlinkBook.Execute(request.Context(), service.BookFolderInput{
		ActorUserID: user.ID, ProjectID: chi.URLParam(request, "projectId"), BookID: chi.URLParam(request, "bookId"),
	}); err != nil {
		writeError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/authormaterials"
	domain "canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"

	"github.com/go-chi/chi/v5"
)

// The bounds of the key of a request of a write of the bridge, as the core bounds its keys.
const (
	minIdempotencyKey = 8
	maxIdempotencyKey = 128
	fingerprintLength = 64
)

type loadRequest struct {
	Books []struct {
		Folder      string `json:"folder"`
		Fingerprint string `json:"fingerprint"`
	} `json:"books"`
	Materials []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"materials"`
	Exclude []string `json:"exclude"`
}

// load is importObsidianBooks (specs/2026-11-14-desk-obsidian-books.md OK1): the choice of the preview and the fingerprint of
// each folder of a book in the body, one Idempotency-Key.
func (handlers Handlers) load(response http.ResponseWriter, request *http.Request) {
	user, proof, ok := handlers.provenWriter(response, request)
	if !ok {
		return
	}
	key, ok := idempotencyKey(response, request)
	if !ok {
		return
	}
	input, err := readLoad(request.Body)
	if err != nil {
		writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation", nil)
		return
	}
	input.ActorUserID, input.ProjectID, input.Key = user.ID, chi.URLParam(request, "projectId"), key
	input.Method, input.Path, input.Proof = request.Method, request.URL.Path, proof
	loaded, err := handlers.Load.Execute(request.Context(), input)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, loadedBodyOf(loaded))
}

func readLoad(body io.Reader) (service.LoadInput, error) {
	decoder := json.NewDecoder(io.LimitReader(body, maxPreviewBody))
	decoder.DisallowUnknownFields()
	var request loadRequest
	if err := decoder.Decode(&request); err != nil || decoder.More() {
		return service.LoadInput{}, errPreviewRequest
	}
	if len(request.Books) == 0 || len(request.Books) > maxChosenFolders || len(request.Materials) > maxChosenFolders ||
		len(request.Exclude) > maxLeftOut {
		return service.LoadInput{}, errPreviewRequest
	}
	input := service.LoadInput{Fingerprints: map[string]string{}, Choice: domain.Choice{Exclude: request.Exclude}}
	for _, book := range request.Books {
		if len(book.Folder) > maxRequestPathLen || !isFingerprint(book.Fingerprint) {
			return service.LoadInput{}, errPreviewRequest
		}
		if _, twice := input.Fingerprints[book.Folder]; twice {
			return service.LoadInput{}, errPreviewRequest
		}
		input.Choice.Books = append(input.Choice.Books, book.Folder)
		input.Fingerprints[book.Folder] = book.Fingerprint
	}
	for _, material := range request.Materials {
		if len(material.Path) > maxRequestPathLen {
			return service.LoadInput{}, errPreviewRequest
		}
		input.Choice.Materials = append(input.Choice.Materials, domain.MaterialFolder{Path: material.Path, Kind: authormaterials.Kind(material.Kind)})
	}
	for _, excluded := range request.Exclude {
		if len(excluded) > maxRequestPathLen {
			return service.LoadInput{}, errPreviewRequest
		}
	}
	return input, nil
}

// isFingerprint is the SHA-256 of a glued text in lower hex, as the preview gives it.
func isFingerprint(value string) bool {
	if len(value) != fingerprintLength {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}

// idempotencyKey is the one key of a write of the bridge (OK1.6, OK2.5): 8 to 128 signs.
func idempotencyKey(response http.ResponseWriter, request *http.Request) (string, bool) {
	keys := request.Header.Values("Idempotency-Key")
	if len(keys) != 1 || len(strings.TrimSpace(keys[0])) < minIdempotencyKey || len(strings.TrimSpace(keys[0])) > maxIdempotencyKey {
		writeProblem(response, http.StatusBadRequest, "Bad Request", "one Idempotency-Key of 8 to 128 signs is required", "request.validation", nil)
		return "", false
	}
	return strings.TrimSpace(keys[0]), true
}

// provenWriter is a writer with the proof of his session the core asks of its writes.
func (handlers Handlers) provenWriter(response http.ResponseWriter, request *http.Request) (auth.User, auth.SessionWriteProof, bool) {
	if request.URL.RawQuery != "" {
		writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation", nil)
		return auth.User{}, auth.SessionWriteProof{}, false
	}
	user, proof, err := handlers.Users.AuthenticateWrite(request.Context(), auth.SessionCookieValue(request), request.Header.Get("X-CSRF-Token"))
	if err != nil {
		writeAuthError(response, err)
		return auth.User{}, auth.SessionWriteProof{}, false
	}
	return user, proof, true
}

type loadedBody struct {
	Queue   *queueBody          `json:"queue"`
	Folders []folderOutcomeBody `json:"folders"`
}

type queueBody struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type folderOutcomeBody struct {
	Folder  string `json:"folder"`
	Outcome string `json:"outcome"`
	BookID  string `json:"bookId,omitempty"`
	Title   string `json:"title,omitempty"`
	QueueID string `json:"queueId,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

func loadedBodyOf(loaded service.Loaded) loadedBody {
	body := loadedBody{Folders: []folderOutcomeBody{}}
	if loaded.Queue != nil {
		body.Queue = &queueBody{ID: loaded.Queue.ID, Status: loaded.Queue.Status}
	}
	for _, folder := range loaded.Folders {
		body.Folders = append(body.Folders, folderOutcomeBody(folder))
	}
	return body
}

// failureStatus is the status of the problem of a load that failed after it made its queue, by its code: a conflict for
// the refusals of the state — the queue, a limit, the link of the vault.
func failureStatus(code string) int {
	switch code {
	case "auth.session_required":
		return http.StatusUnauthorized
	case "auth.email_unverified", "forbidden":
		return http.StatusForbidden
	case "not_found":
		return http.StatusNotFound
	case "server.error":
		return http.StatusInternalServerError
	}
	return http.StatusConflict
}

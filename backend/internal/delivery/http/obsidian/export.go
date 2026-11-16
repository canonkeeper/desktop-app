// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"

	"github.com/go-chi/chi/v5"
)

// maxExportBody bounds the body of an export: one word of the author.
const maxExportBody = 1 << 10

// ExportUseCase is exportObsidianSaga.
type ExportUseCase interface {
	Execute(ctx context.Context, input service.ExportInput) (service.Exported, error)
}

type exportRequest struct {
	ReplaceChanged bool `json:"replaceChanged"`
}

// export is exportObsidianSaga (specs/2026-11-15-desk-obsidian-export.md OE1): the word of the author to replace what he
// changed in the body, one Idempotency-Key; the answer is the result of the run, complete or not.
func (handlers Handlers) export(response http.ResponseWriter, request *http.Request) {
	user, proof, ok := handlers.provenWriter(response, request)
	if !ok {
		return
	}
	key, ok := idempotencyKey(response, request)
	if !ok {
		return
	}
	body, err := readExport(request.Body)
	if err != nil {
		writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation", nil)
		return
	}
	exported, err := handlers.Export.Execute(request.Context(), service.ExportInput{
		ActorUserID: user.ID, ProjectID: chi.URLParam(request, "projectId"), ReplaceChanged: body.ReplaceChanged, Key: key, Proof: proof,
	})
	var notReady domain.MapNotReady
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeAuthError(response, err)
	case errors.As(err, &notReady):
		writeProblem(response, http.StatusConflict, "Conflict", "the map of the saga is not ready", domain.CodeMapNotReady, func(body *problemBody) {
			body.Reason = string(notReady.Reason)
		})
	case errors.Is(err, domain.ErrExportDirUnsafe):
		writeProblem(response, http.StatusConflict, "Conflict", "the path to the folder of the export is not a folder", domain.CodeExportDirUnsafe, nil)
	case errors.Is(err, domain.ErrExportManifestUnreadable):
		writeProblem(response, http.StatusConflict, "Conflict", "the manifest of the export does not read", domain.CodeExportManifestUnreadable, nil)
	case err != nil:
		writeError(response, err)
	default:
		writeJSON(response, http.StatusOK, exportedBodyOf(exported))
	}
}

// readExport reads the body of an export: an object with the one optional word — the body the contract requires.
func readExport(body io.Reader) (exportRequest, error) {
	decoder := json.NewDecoder(io.LimitReader(body, maxExportBody))
	decoder.DisallowUnknownFields()
	var request exportRequest
	if err := decoder.Decode(&request); err != nil || decoder.More() {
		return exportRequest{}, errPreviewRequest
	}
	return request, nil
}

type exportGroupBody struct {
	Count int      `json:"count"`
	Names []string `json:"names"`
}

type exportSkipBody struct {
	Reason string   `json:"reason"`
	Count  int      `json:"count"`
	Names  []string `json:"names"`
}

type exportedBody struct {
	Folder    string           `json:"folder"`
	At        time.Time        `json:"at"`
	Complete  bool             `json:"complete"`
	Created   exportGroupBody  `json:"created"`
	Updated   exportGroupBody  `json:"updated"`
	Unchanged exportGroupBody  `json:"unchanged"`
	Moved     exportGroupBody  `json:"moved"`
	Kept      exportGroupBody  `json:"kept"`
	Skipped   []exportSkipBody `json:"skipped"`
}

func exportedBodyOf(exported service.Exported) exportedBody {
	group := func(group service.ExportGroup) exportGroupBody {
		return exportGroupBody{Count: group.Count, Names: append([]string{}, group.Names...)}
	}
	body := exportedBody{
		Folder: exported.Folder, At: exported.At.UTC(), Complete: exported.Complete, Created: group(exported.Created),
		Updated: group(exported.Updated), Unchanged: group(exported.Unchanged), Moved: group(exported.Moved), Kept: group(exported.Kept),
		Skipped: []exportSkipBody{},
	}
	for reason, skipped := range exported.Skipped {
		if skipped.Count > 0 {
			body.Skipped = append(body.Skipped, exportSkipBody{Reason: string(reason), Count: skipped.Count, Names: append([]string{}, skipped.Names...)})
		}
	}
	slices.SortFunc(body.Skipped, func(left, right exportSkipBody) int {
		switch {
		case left.Reason < right.Reason:
			return -1
		case left.Reason > right.Reason:
			return 1
		}
		return 0
	})
	return body
}

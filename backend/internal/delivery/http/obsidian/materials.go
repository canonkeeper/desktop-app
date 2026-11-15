// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/authormaterials"
	domain "canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"

	"github.com/go-chi/chi/v5"
)

// The bounds of a request of a load of records: every record of the preview with what the author saw of it.
const (
	maxMaterialsBody = 4 << 20
	seenLength       = 16
)

type materialsRequest struct {
	Books     []string `json:"books"`
	Materials []struct {
		Path        string `json:"path"`
		Kind        string `json:"kind"`
		Fingerprint string `json:"fingerprint"`
	} `json:"materials"`
	Exclude []string `json:"exclude"`
	Records []struct {
		Path     string `json:"path"`
		Seen     string `json:"seen"`
		Decision string `json:"decision"`
	} `json:"records"`
	Replace []string `json:"replace"`
}

// materials is importObsidianMaterials (specs/2026-11-15-desk-obsidian-notes.md ON1): the choice of the preview with the
// fingerprint of each folder of materials, what the author saw of each record and his decisions in the body, one
// Idempotency-Key.
func (handlers Handlers) materials(response http.ResponseWriter, request *http.Request) {
	user, proof, ok := handlers.provenWriter(response, request)
	if !ok {
		return
	}
	key, ok := idempotencyKey(response, request)
	if !ok {
		return
	}
	input, err := readMaterials(request.Body)
	if err != nil {
		writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation", nil)
		return
	}
	input.ActorUserID, input.ProjectID, input.Key, input.Proof = user.ID, chi.URLParam(request, "projectId"), key, proof
	loaded, err := handlers.Materials.Execute(request.Context(), input)
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeAuthError(response, err)
	case errors.Is(err, service.ErrMaterialsFingerprint):
		writeProblem(response, http.StatusBadRequest, "Bad Request", "a folder of materials has no fingerprint", "request.validation", nil)
	case errors.Is(err, service.ErrMaterialsSeen):
		writeProblem(response, http.StatusBadRequest, "Bad Request", "a record of the load has no seen", "request.validation", nil)
	case err != nil:
		writeError(response, err)
	default:
		writeJSON(response, http.StatusOK, loadedMaterialsBodyOf(loaded))
	}
}

func readMaterials(body io.Reader) (service.MaterialsInput, error) {
	decoder := json.NewDecoder(io.LimitReader(body, maxMaterialsBody))
	decoder.DisallowUnknownFields()
	var request materialsRequest
	if err := decoder.Decode(&request); err != nil || decoder.More() {
		return service.MaterialsInput{}, errPreviewRequest
	}
	if len(request.Materials) == 0 || len(request.Materials) > maxChosenFolders || len(request.Books) > maxChosenFolders ||
		len(request.Exclude) > maxLeftOut || len(request.Records) > domain.MaxRecords {
		return service.MaterialsInput{}, errPreviewRequest
	}
	input := service.MaterialsInput{
		Choice: domain.Choice{Books: request.Books, Exclude: request.Exclude}, Fingerprints: map[string]string{}, Seen: map[string]string{},
		Decisions: domain.Decisions{Notes: map[string]domain.Decision{}, States: map[string]domain.Decision{}},
	}
	for _, folder := range append(append([]string{}, request.Books...), request.Exclude...) {
		if len(folder) > maxRequestPathLen {
			return service.MaterialsInput{}, errPreviewRequest
		}
	}
	for _, material := range request.Materials {
		if _, twice := input.Fingerprints[material.Path]; twice || len(material.Path) > maxRequestPathLen || !isFingerprint(material.Fingerprint) {
			return service.MaterialsInput{}, errPreviewRequest
		}
		input.Choice.Materials = append(input.Choice.Materials, domain.MaterialFolder{Path: material.Path, Kind: authormaterials.Kind(material.Kind)})
		input.Fingerprints[material.Path] = material.Fingerprint
	}
	for _, record := range request.Records {
		if _, twice := input.Seen[record.Path]; twice || len(record.Path) > maxRequestPathLen || !isSeen(record.Seen) {
			return service.MaterialsInput{}, errPreviewRequest
		}
		input.Seen[record.Path] = record.Seen
		switch decision := domain.Decision(record.Decision); decision {
		case "":
		case domain.DecisionReplace, domain.DecisionKeep:
			input.Decisions.Notes[record.Path] = decision
		default:
			return service.MaterialsInput{}, errPreviewRequest
		}
	}
	for _, state := range request.Replace {
		if state != domain.StateEditedInApp && state != domain.StateOtherSource {
			return service.MaterialsInput{}, errPreviewRequest
		}
		input.Decisions.States[state] = domain.DecisionReplace
	}
	return input, nil
}

// isSeen is what a preview says the author sees of the state of a record.
func isSeen(value string) bool {
	return len(value) == seenLength && strings.Trim(value, "0123456789abcdef") == ""
}

type noteGroupBody struct {
	Count int      `json:"count"`
	Names []string `json:"names"`
}

type loadedMaterialsBody struct {
	Created   noteGroupBody `json:"created"`
	Updated   noteGroupBody `json:"updated"`
	Replaced  noteGroupBody `json:"replaced"`
	Unchanged noteGroupBody `json:"unchanged"`
	Kept      struct {
		Taken   noteGroupBody `json:"taken"`
		Edited  noteGroupBody `json:"edited"`
		Changed noteGroupBody `json:"changed"`
	} `json:"kept"`
	Skipped []skipBody `json:"skipped"`
}

func loadedMaterialsBodyOf(loaded service.MaterialsLoaded) loadedMaterialsBody {
	group := func(group service.NoteGroup) noteGroupBody {
		return noteGroupBody{Count: group.Count, Names: append([]string{}, group.Names...)}
	}
	body := loadedMaterialsBody{
		Created: group(loaded.Created), Updated: group(loaded.Updated), Replaced: group(loaded.Replaced), Unchanged: group(loaded.Unchanged),
		Skipped: skippedBody(loaded.Skipped),
	}
	body.Kept.Taken, body.Kept.Edited, body.Kept.Changed = group(loaded.Kept.Taken), group(loaded.Kept.Edited), group(loaded.Kept.Changed)
	return body
}

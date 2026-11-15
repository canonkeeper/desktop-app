// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"errors"
	"net/http"

	domain "canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/aisettings"
)

// codeNotSet is a question to a provider the settings do not name (PB1.1).
const codeNotSet = "ai.not_set"

// modelsJSON is AiModels of the contract.
type modelsJSON struct {
	Outcome string   `json:"outcome"`
	Models  []string `json:"models"`
	Reason  *string  `json:"reason"`
}

// aiModels is listAiModels (specs/2026-11-13-desk-probes.md PB1): the names of the models of the provider of the saved
// settings, that it gives none, or the code of the reason it failed — never its words. Nothing of it is kept.
func (handlers Handlers) aiModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	models, err := handlers.AI.Models.Execute(r.Context())
	if errors.Is(err, service.ErrNotForSource) {
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the helper of the account has no models to list", codeNotForSource)
		return
	}
	if errors.Is(err, service.ErrNotSet) {
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "no provider to ask", codeNotSet)
		return
	}
	if err != nil {
		unexpected(w, r, err)
		return
	}
	response := modelsJSON{Outcome: string(models.Outcome), Models: models.Names, Reason: reasonJSON(models.Reason)}
	if response.Models == nil {
		response.Models = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

// reasonJSON is the code of a reason, null when there is none.
func reasonJSON(reason domain.Reason) *string {
	if reason == "" {
		return nil
	}
	code := string(reason)
	return &code
}

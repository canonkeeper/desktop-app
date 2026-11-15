// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	domain "canonkeeper/backend/internal/domain/desktop"
	"canonkeeper/backend/internal/httpapi"
)

// Updates are the operations of an update of the app (services/desktop: ReadAppUpdate, CheckAppUpdate,
// InstallAppUpdate, CancelAppUpdate, SetAppUpdateDaily).
type Updates struct {
	Read interface {
		Execute(context.Context) domain.AppUpdate
	}
	Check    updateCommand
	Install  updateCommand
	Cancel   updateCommand
	SetDaily interface {
		Execute(ctx context.Context, daily bool) (domain.AppUpdate, error)
	}
}

type updateCommand interface {
	Execute(context.Context) (domain.AppUpdate, error)
}

type releaseResponse struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
}

type appUpdateResponse struct {
	Version   *string          `json:"version"`
	Daily     bool             `json:"daily"`
	Kind      string           `json:"kind"`
	Phase     string           `json:"phase"`
	Found     *string          `json:"found"`
	Available *releaseResponse `json:"available"`
	Percent   *int             `json:"percent"`
	Refusal   *string          `json:"refusal"`
	Package   *string          `json:"package"`
	CheckedAt *string          `json:"checkedAt"`
}

type appUpdateSettingsJSON struct {
	Daily *bool `json:"daily"`
}

// appUpdate is getAppUpdate (specs/2026-11-13-desk-updates.md UB1.7): the version of the program, the daily check, how
// the app was installed and the phase of an update with what goes with it, to the session of the window.
func (handlers Handlers) appUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	writeAppUpdate(w, handlers.Updates.Read.Execute(r.Context()))
}

func (handlers Handlers) checkAppUpdate(w http.ResponseWriter, r *http.Request) {
	handlers.updateCommand(w, r, handlers.Updates.Check)
}

func (handlers Handlers) installAppUpdate(w http.ResponseWriter, r *http.Request) {
	handlers.updateCommand(w, r, handlers.Updates.Install)
}

func (handlers Handlers) cancelAppUpdate(w http.ResponseWriter, r *http.Request) {
	handlers.updateCommand(w, r, handlers.Updates.Cancel)
}

// updateCommand runs a write of an update without a body; a phase that does not allow it is 409 desktop.update_state.
func (handlers Handlers) updateCommand(w http.ResponseWriter, r *http.Request, command updateCommand) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	update, err := command.Execute(r.Context())
	writeUpdateResult(w, r, update, err)
}

// updateAppUpdateSettings turns the daily check on or off; off makes no request at all (UB1.2).
func (handlers Handlers) updateAppUpdateSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	var request appUpdateSettingsJSON
	if _, ok := decode(r, &request); !ok || request.Daily == nil {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "invalid request body", "validation.failed")
		return
	}
	update, err := handlers.Updates.SetDaily.Execute(r.Context(), *request.Daily)
	writeUpdateResult(w, r, update, err)
}

func writeUpdateResult(w http.ResponseWriter, r *http.Request, update domain.AppUpdate, err error) {
	switch {
	case errors.Is(err, domain.ErrUpdateState):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the update is in another phase", "desktop.update_state")
	case err != nil:
		unexpected(w, r, err)
	default:
		writeAppUpdate(w, update)
	}
}

func writeAppUpdate(w http.ResponseWriter, update domain.AppUpdate) {
	response := appUpdateResponse{Daily: update.Daily, Kind: string(update.Kind), Phase: string(update.Phase)}
	if update.Version != "" {
		response.Version = &update.Version
	}
	if update.Phase == domain.UpdateIdle {
		found := string(update.Found)
		response.Found = &found
	}
	if update.Available != nil {
		response.Available = &releaseResponse{Version: update.Available.Version, Notes: update.Available.Notes}
	}
	if update.Phase == domain.UpdateDownloading {
		percent := update.Percent
		response.Percent = &percent
	}
	if update.Phase == domain.UpdateInstallRefused {
		refusal := string(update.Refusal)
		response.Refusal = &refusal
	}
	if update.Package != "" && (update.Phase == domain.UpdateInstallRefused || update.Phase == domain.UpdateInstallFailed) {
		response.Package = &update.Package
	}
	if !update.CheckedAt.IsZero() {
		checked := update.CheckedAt.UTC().Format(time.RFC3339)
		response.CheckedAt = &checked
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

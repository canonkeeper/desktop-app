// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/desktop"
)

// ReadState reads the state of the app (services/desktop.ReadAppState).
type ReadState interface {
	Execute(ctx context.Context) (service.AppState, error)
}

type usageResponse struct {
	// Bytes is null until the first count ended.
	Bytes    *int64 `json:"bytes"`
	Counting bool   `json:"counting"`
}

type backupResponse struct {
	MadeAt  string  `json:"madeAt"`
	Version *string `json:"version"`
}

type networkResponse struct {
	Host    string `json:"host"`
	Purpose string `json:"purpose"`
}

type stateResponse struct {
	Version *string           `json:"version"`
	DataDir string            `json:"dataDir"`
	Usage   usageResponse     `json:"usage"`
	Backups []backupResponse  `json:"backups"`
	Network []networkResponse `json:"network"`
}

// state is getDesktopState (specs/2026-11-13-desk-settings.md ST2.1): what «Настройки» of the app show — the version,
// the data directory and its place, the copies before an update and the hosts of the network — to the session of the
// window; without it, the 401 of the site.
func (handlers Handlers) state(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, err := handlers.Sessions.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if errors.Is(err, auth.ErrUnauthenticated) {
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
		return
	}
	if err != nil {
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected error", "server.error")
		return
	}
	state, err := handlers.State.Execute(r.Context())
	if err != nil {
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected error", "server.error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(stateResponseOf(state))
}

func stateResponseOf(state service.AppState) stateResponse {
	response := stateResponse{
		Version: textOrNull(state.Version), DataDir: state.DataDir,
		Usage:   usageResponse{Counting: state.Usage.Counting},
		Backups: make([]backupResponse, 0, len(state.Backups)),
		Network: make([]networkResponse, 0, len(state.Network)),
	}
	if state.Usage.Known {
		bytes := state.Usage.Bytes
		response.Usage.Bytes = &bytes
	}
	for _, backup := range state.Backups {
		response.Backups = append(response.Backups, backupResponse{
			MadeAt: backup.Made.UTC().Format("2006-01-02T15:04:05.000Z07:00"), Version: textOrNull(backup.Version),
		})
	}
	for _, use := range state.Network {
		response.Network = append(response.Network, networkResponse{Host: use.Host, Purpose: use.Purpose})
	}
	return response
}

func textOrNull(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}

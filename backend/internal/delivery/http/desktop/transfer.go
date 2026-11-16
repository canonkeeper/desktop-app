// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/accountlink"
	"canonkeeper/backend/internal/services/sagaarchive"

	"github.com/go-chi/chi/v5"
)

// TransferSessions are the sessions of the window as the transfer needs them: the start keeps the proof of the write of the
// session that started it, for the open of the saga file.
type TransferSessions interface {
	CurrentUser(ctx context.Context, cookieValue string) (auth.User, error)
	AuthenticateWrite(ctx context.Context, cookieValue, csrfToken string) (auth.User, auth.SessionWriteProof, error)
}

// SagaTransfer are the operations of the window of the transfer of a saga from the site
// (specs/2026-11-16-desk-site-saga.md TR2): its state, its start and its stop, under the session of the window; the writes
// under its strict Origin and its CSRF token.
type SagaTransfer struct {
	Sessions TransferSessions
	Start    interface {
		Execute(ctx context.Context, actor string, proof auth.SessionWriteProof) (service.Transfer, error)
	}
	Read   interface{ Execute() service.Transfer }
	Cancel interface {
		Execute(id string) (service.Transfer, error)
	}
	AllowedOrigins []string
}

func (transfer SagaTransfer) Mount(router chi.Router) {
	if transfer.Sessions == nil || transfer.Start == nil || transfer.Read == nil || transfer.Cancel == nil {
		panic("desktop HTTP: dependencies of the transfer are required")
	}
	router.Get("/desktop/saga-transfer", transfer.read)
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginRequiredMiddleware(transfer.AllowedOrigins...))
		router.Post("/desktop/saga-transfer", transfer.start)
		router.Delete("/desktop/saga-transfer", transfer.cancel)
	})
}

// sagaTransferJSON is SiteSagaTransfer of the contract.
type sagaTransferJSON struct {
	ID        *string             `json:"id"`
	Phase     string              `json:"phase"`
	Address   *string             `json:"address"`
	ExpiresAt *string             `json:"expiresAt"`
	Email     *string             `json:"email"`
	Name      *string             `json:"name"`
	Bytes     *int64              `json:"bytes"`
	Code      *string             `json:"code"`
	ProjectID *string             `json:"projectId"`
	Opened    *sagaarchive.Opened `json:"opened"`
}

func (transfer SagaTransfer) read(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, err := transfer.Sessions.CurrentUser(r.Context(), auth.SessionCookieValue(r)); err != nil {
		writeSessionError(w, r, err)
		return
	}
	writeTransfer(w, transfer.Read.Execute())
}

// start is startSiteSagaTransfer (TR2.1): the proof of the write of the session of the window goes with the transfer.
func (transfer SagaTransfer) start(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, proof, err := transfer.Sessions.AuthenticateWrite(r.Context(), auth.SessionCookieValue(r), r.Header.Get("X-CSRF-Token"))
	if err != nil {
		writeSessionError(w, r, err)
		return
	}
	started, err := transfer.Start.Execute(r.Context(), user.ID, proof)
	switch {
	case errors.Is(err, domain.ErrUpdating):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "an update of the app is installed", "desktop.updating")
	case errors.Is(err, domain.ErrTransferRunning):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "a transfer downloads or opens", "saga_transfer.running")
	case errors.Is(err, domain.ErrNotLinked):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the app is not linked with an account", "account.not_linked")
	case err != nil:
		unexpected(w, r, err)
	default:
		writeTransfer(w, started)
	}
}

// transferCancelJSON is SiteSagaTransferCancel of the contract: the transfer the page stops or whose outcome it puts away.
type transferCancelJSON struct {
	ID *string `json:"id"`
}

// transferID is the shape of the id of a transfer: a UUID in lower case, as the backend makes it.
var transferID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// cancel is cancelSiteSagaTransfer (TR2.2) of the transfer the body names: one that opens is 409 saga_transfer.opening;
// another transfer — one started meanwhile — is answered as it is.
func (transfer SagaTransfer) cancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, _, err := transfer.Sessions.AuthenticateWrite(r.Context(), auth.SessionCookieValue(r), r.Header.Get("X-CSRF-Token")); err != nil {
		writeSessionError(w, r, err)
		return
	}
	var request transferCancelJSON
	if _, ok := decode(r, &request); !ok || request.ID == nil || !transferID.MatchString(*request.ID) {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "invalid request body", "validation.failed")
		return
	}
	after, err := transfer.Cancel.Execute(*request.ID)
	if errors.Is(err, domain.ErrTransferOpening) {
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the saga already opens", "saga_transfer.opening")
		return
	}
	if err != nil {
		unexpected(w, r, err)
		return
	}
	writeTransfer(w, after)
}

func writeSessionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, auth.ErrInvalidCSRF):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
	default:
		unexpected(w, r, err)
	}
}

func writeTransfer(w http.ResponseWriter, transfer service.Transfer) {
	text := func(value string) *string {
		if value == "" {
			return nil
		}
		return &value
	}
	response := sagaTransferJSON{ID: text(transfer.ID), Phase: string(transfer.Phase), Code: text(transfer.Code), ProjectID: text(transfer.ProjectID), Opened: transfer.Opened}
	switch transfer.Phase {
	case domain.TransferWaiting:
		response.Address, response.Email = text(transfer.Address), text(transfer.Email)
		expires := moment(transfer.Expires)
		response.ExpiresAt = &expires
	case domain.TransferDownloading, domain.TransferOpening:
		bytes := transfer.Bytes
		response.Name, response.Bytes = text(transfer.Name), &bytes
	case domain.TransferDone:
		response.Name = text(transfer.Name)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

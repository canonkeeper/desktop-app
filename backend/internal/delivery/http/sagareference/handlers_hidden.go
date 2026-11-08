package sagareference

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/sagareference"

	"github.com/go-chi/chi/v5"
)

const maxHideBody = 4 << 10

type hiddenItemDTO struct {
	ID        string  `json:"id"`
	NodeKey   string  `json:"nodeKey"`
	NodeName  string  `json:"nodeName"`
	OtherKey  *string `json:"otherKey"`
	OtherName *string `json:"otherName"`
	CreatedAt string  `json:"createdAt"`
}

func hiddenDTO(item sagamemory.MapHiddenItem) hiddenItemDTO {
	dto := hiddenItemDTO{ID: item.ID, NodeKey: item.NodeKey, NodeName: item.NodeName, CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if item.OtherKey != "" {
		dto.OtherKey, dto.OtherName = &item.OtherKey, &item.OtherName
	}
	return dto
}

// MapHidden serves listSagaMapHidden.
func (handlers Handlers) MapHidden(w http.ResponseWriter, request *http.Request) {
	scope, _, err := handlers.scope(request)
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	items, err := handlers.hidden.List.Execute(request.Context(), scope)
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	dto := make([]hiddenItemDTO, len(items))
	for i, item := range items {
		dto[i] = hiddenDTO(item)
	}
	writeJSON(w, struct {
		Items []hiddenItemDTO `json:"items"`
	}{dto})
}

// writer checks the write proof of the session; the role is checked in the transaction of the write.
func (handlers Handlers) writer(request *http.Request) (service.MapWrite, error) {
	user, proof, err := handlers.currentUser.AuthenticateWrite(request.Context(), auth.SessionCookieValue(request), request.Header.Get("X-CSRF-Token"))
	if err != nil {
		return service.MapWrite{}, err
	}
	return service.MapWrite{
		Scope: service.Scope{ActorUserID: user.ID, ProjectID: strings.TrimSpace(chi.URLParam(request, "projectId")), View: request.URL.Query().Get("sourceView")},
		Proof: proof,
	}, nil
}

// HideOnMap serves hideSagaMapItem.
func (handlers Handlers) HideOnMap(w http.ResponseWriter, request *http.Request) {
	write, err := handlers.writer(request)
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	var body struct {
		NodeKey  string `json:"nodeKey"`
		OtherKey string `json:"otherKey"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, request.Body, maxHideBody))
	if err != nil || !utf8.Valid(raw) {
		writeError(w, request.Context(), service.ErrInvalid)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		writeError(w, request.Context(), service.ErrInvalid)
		return
	}
	item, err := handlers.hidden.Hide.Execute(request.Context(), write, service.HideInput{NodeKey: body.NodeKey, OtherKey: body.OtherKey})
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, hiddenDTO(item))
}

// RestoreOnMap serves restoreSagaMapItem.
func (handlers Handlers) RestoreOnMap(w http.ResponseWriter, request *http.Request) {
	write, err := handlers.writer(request)
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	if err := handlers.hidden.Restore.Execute(request.Context(), write, chi.URLParam(request, "hiddenId")); err != nil {
		writeError(w, request.Context(), err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

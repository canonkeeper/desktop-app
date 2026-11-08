package notes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/notes"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/notes"
	"github.com/go-chi/chi/v5"
)

type CurrentUser interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}
type Handlers struct {
	List           service.List
	Get            service.Get
	Create         service.Create
	Update         service.Update
	Delete         service.Delete
	Resolve        service.Resolve
	Auth           CurrentUser
	AllowedOrigins []string
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(handlers.AllowedOrigins...))
		router.Post("/notes/query", handlers.list)
		router.Get("/personal-notes/{noteId}", handlers.get)
		router.Get("/personal-notes/{noteId}/source", handlers.resolve)
		router.Post("/personal-notes", handlers.create)
		router.Patch("/personal-notes/{noteId}", handlers.update)
		router.Delete("/personal-notes/{noteId}", handlers.delete)
	})
}
func (handlers Handlers) actor(w http.ResponseWriter, r *http.Request) (service.Write, bool) {
	var user auth.User
	var proof auth.SessionWriteProof
	var err error
	if httpapi.RequiresCSRF(r.Method) {
		user, proof, err = handlers.Auth.AuthenticateWrite(r.Context(), auth.SessionCookieValue(r), r.Header.Get("X-CSRF-Token"))
	} else {
		user, err = handlers.Auth.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	}
	if err != nil {
		writeError(w, err)
		return service.Write{}, false
	}
	return service.Write{UserID: user.ID, Proof: proof}, true
}
func (handlers Handlers) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, domain.ErrValidation)
		return
	}
	var input service.ListInput
	if err := decode(w, r, &input); err != nil {
		writeError(w, err)
		return
	}
	value, err := handlers.List.Execute(r.Context(), actor.UserID, input)
	respond(w, http.StatusOK, value, err)
}
func (handlers Handlers) get(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.actor(w, r)
	if !ok {
		return
	}
	value, err := handlers.Get.Execute(r.Context(), actor.UserID, chi.URLParam(r, "noteId"))
	respond(w, http.StatusOK, value, err)
}
func (handlers Handlers) resolve(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.actor(w, r)
	if !ok {
		return
	}
	value, err := handlers.Resolve.Execute(r.Context(), actor.UserID, chi.URLParam(r, "noteId"))
	respond(w, http.StatusOK, value, err)
}
func (handlers Handlers) create(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.actor(w, r)
	if !ok {
		return
	}
	var request struct {
		Title  optionalText   `json:"title"`
		Body   string         `json:"body"`
		Source *domain.Source `json:"source"`
	}
	if err := decode(w, r, &request); err != nil {
		writeError(w, err)
		return
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 {
		writeError(w, domain.ErrValidation)
		return
	}
	input := service.CreateInput{Body: request.Body, Source: request.Source}
	if request.Title.Value != nil {
		input.Title = *request.Title.Value
	}
	value, err := handlers.Create.Execute(r.Context(), actor, input, r.Header.Get("Idempotency-Key"), r.Method, r.URL.Path)
	respond(w, http.StatusCreated, value, err)
}
func (handlers Handlers) update(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.actor(w, r)
	if !ok {
		return
	}
	var request struct {
		Title            optionalText `json:"title"`
		Body             optionalText `json:"body"`
		ExpectedRevision int64        `json:"expectedRevision"`
	}
	if err := decode(w, r, &request); err != nil {
		writeError(w, err)
		return
	}
	value, err := handlers.Update.Execute(r.Context(), actor, chi.URLParam(r, "noteId"),
		service.UpdateInput{Body: request.Body.Value, Title: request.Title.Value, ExpectedRevision: request.ExpectedRevision})
	respond(w, http.StatusOK, value, err)
}
func (handlers Handlers) delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.actor(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()["expectedRevision"]
	if len(values) != 1 {
		writeError(w, domain.ErrValidation)
		return
	}
	revision, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		writeError(w, domain.ErrValidation)
		return
	}
	err = handlers.Delete.Execute(r.Context(), actor, chi.URLParam(r, "noteId"), revision)
	respond(w, http.StatusNoContent, nil, err)
}

type optionalText struct{ Value *string }

func (value *optionalText) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return domain.ErrValidation
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return err
	}
	value.Value = &text
	return nil
}
func decode(w http.ResponseWriter, r *http.Request, target any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil || !utf8.Valid(raw) {
		return domain.ErrValidation
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return domain.ErrValidation
	}
	for name, value := range fields {
		if name != "source" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return domain.ErrValidation
		}
	}
	if value, present := fields["limit"]; present {
		var limit int
		if json.Unmarshal(value, &limit) != nil || limit < 1 {
			return domain.ErrValidation
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return domain.ErrValidation
	}
	return nil
}
func respond(w http.ResponseWriter, status int, value any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "server.error"
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		status, code = http.StatusUnauthorized, "auth.session_required"
	case errors.Is(err, auth.ErrInvalidCSRF):
		status, code = http.StatusForbidden, "csrf.invalid"
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrValidation):
		status, code = http.StatusBadRequest, "request.validation"
	case errors.Is(err, domain.ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, domain.ErrUnavailable):
		status, code = http.StatusServiceUnavailable, "source.unavailable"
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(w, "", status, http.StatusText(status), http.StatusText(status), code)
}

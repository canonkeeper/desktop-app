// SPDX-License-Identifier: AGPL-3.0-only

// Package desktop is the HTTP of the desktop build (specs/2026-11-07-desktop-app.md DA3): the operation that opens the
// session of the window and «кто автор» of the window (specs/2026-11-12-desk-window.md WN1.1).
package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/desktop"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/desktop"

	"github.com/go-chi/chi/v5"
)

// OpenSession exchanges a one-time code for the session of the local author.
type OpenSession interface {
	Execute(ctx context.Context, code string) (service.OpenedSession, error)
}

// Sessions read the user of the session of a cookie: the core's sessions (auth.Service).
type Sessions interface {
	CurrentUser(ctx context.Context, cookieValue string) (auth.User, error)
}

// Handlers mount openDesktopSession behind the strict Origin of the window and the CSRF check of the core, and
// getCurrentUser, which names the local author of the session of the window.
type Handlers struct {
	Open           OpenSession
	Sessions       Sessions
	CSRF           func(http.Handler) http.Handler
	AllowedOrigins []string
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Get("/auth/session", handlers.currentUser)
	router.With(httpapi.StrictOriginRequiredMiddleware(handlers.AllowedOrigins...), handlers.CSRF).
		Post("/desktop/session", handlers.openSession)
}

type openSessionRequest struct {
	Code string `json:"code"`
}

type userResponse struct {
	ID            string `json:"id"`
	DisplayName   string `json:"displayName"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"emailVerified"`
	// DocumentsAccepted is true: the desktop has no documents of the site to accept (DA2.7).
	DocumentsAccepted bool    `json:"documentsAccepted"`
	AvatarURL         *string `json:"avatarUrl"`
	CreatedAt         string  `json:"createdAt"`
}

type sessionResponse struct {
	User userResponse `json:"user"`
}

// openSession is openDesktopSession (DA3.3): the code of the shell becomes the cookie of the window — HttpOnly,
// SameSite=Strict, no term, no Secure on the loopback — and the answer names the local author. A bad body is 400, a
// code that does not open the window 403; neither says why.
func (handlers Handlers) openSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	raw, err := io.ReadAll(httpapi.JSONBody(r))
	var request openSessionRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || decoder.Decode(&request) != nil || request.Code == "" {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "invalid request body", "validation.failed")
		return
	}
	opened, err := handlers.Open.Execute(r.Context(), request.Code)
	if errors.Is(err, domain.ErrInvalidOpenCode) {
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "the window was not opened", "desktop.open_code_invalid")
		return
	}
	if err != nil {
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected error", "server.error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookieName, Value: opened.Session.CookieValue, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(sessionResponse{User: userOf(opened.User)})
}

// currentUser is getCurrentUser of the desktop (specs/2026-11-12-desk-window.md WN1.1): the root of the web asks it on every
// page, and the window answers with the local author of its session in the form of the site. Without the session it is
// the 401 of the site.
func (handlers Handlers) currentUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, err := handlers.Sessions.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if errors.Is(err, auth.ErrUnauthenticated) {
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
		return
	}
	if err != nil {
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected error", "server.error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(userOf(user))
}

// userOf is the local author in the form User of the contract.
func userOf(user auth.User) userResponse {
	return userResponse{
		ID: user.ID, DisplayName: user.DisplayName, Email: user.Email, EmailVerified: user.EmailVerified,
		DocumentsAccepted: true, CreatedAt: user.CreatedAt.Format("2006-01-02T15:04:05.000Z07:00"),
	}
}

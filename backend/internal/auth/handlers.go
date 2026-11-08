package auth

import (
	"encoding/json"
	"net/http"

	"canonkeeper/backend/internal/httpapi"

	"github.com/go-chi/chi/v5"
)

const SessionCookieName = "ck_session"

// Handlers are the core's HTTP parts: the CSRF token, the CSRF check and the session cookie.
type Handlers struct {
	service       *Service
	secureCookies bool
}

func NewHandlers(service *Service, secureCookies bool) Handlers {
	return Handlers{service: service, secureCookies: secureCookies}
}

func (h Handlers) Mount(router chi.Router) {
	router.Get("/auth/csrf", h.GetCSRFToken)
}

func (h Handlers) GetCSRFToken(w http.ResponseWriter, r *http.Request) {
	// Issuing a token replaces the CSRF secret of the session, so a page of another site must not
	// do it by sending the browser here (SEC-15, specs/2026-10-28-security-hardening.md R2.3).
	if httpapi.CrossSiteFetch(r) {
		writeAuthProblem(w, http.StatusForbidden, "Forbidden", "invalid request origin", "origin.invalid")
		return
	}
	token, err := h.service.CSRFToken(r.Context(), SessionCookieValue(r))
	if err != nil {
		writeAuthProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected auth error", "server.error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if token.ClearSessionCookie {
		http.SetCookie(w, h.ClearedSessionCookie())
	}
	writeJSON(w, http.StatusOK, csrfTokenResponse{
		CSRFToken: token.Token,
		ExpiresAt: token.ExpiresAt.Format("2006-01-02T15:04:05.000Z07:00"),
		Rotation:  token.Rotation,
	})
}

func (h Handlers) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpapi.RequiresCSRF(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		csrfToken := r.Header.Get("X-CSRF-Token")
		if csrfToken == "" {
			writeAuthProblem(w, http.StatusForbidden, "Forbidden", "missing CSRF token", "csrf.required")
			return
		}
		if err := h.service.ValidateCSRF(r.Context(), SessionCookieValue(r), csrfToken); err != nil {
			writeAuthProblem(w, http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SessionCookie outlives the browser only for a persistent session; any other one is
// a browser-session cookie without Expires or Max-Age (R3.3.1).
func (h Handlers) SessionCookie(session Session) *http.Cookie {
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    session.CookieValue,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
	if session.Persistent {
		cookie.Expires = session.ExpiresAt
	}
	return cookie
}

func (h Handlers) ClearedSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

type csrfTokenResponse struct {
	CSRFToken string `json:"csrfToken"`
	ExpiresAt string `json:"expiresAt"`
	Rotation  string `json:"rotation"`
}

// SessionCookieValue is the session cookie of the request; a child cookie without its
// pair is no session (child_session.go).
func SessionCookieValue(r *http.Request) string {
	value := RawSessionCookieValue(r)
	if IsImpersonationChildCookie(value) && !matchesImpersonationChildPair(value, ImpersonatorCookieValue(r)) {
		return ""
	}
	return value
}

// RawSessionCookieValue is the session cookie as the browser sent it, before the pair check.
func RawSessionCookieValue(r *http.Request) string {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAuthProblem(w http.ResponseWriter, status int, title, detail, code string) {
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(w, "", status, title, detail, code)
}

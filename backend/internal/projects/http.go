package projects

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/httpapi"

	"github.com/go-chi/chi/v5"
)

type ProjectService interface {
	ListProjects(context.Context, string, ListProjectsInput) (ProjectPage, error)
	CreateProject(context.Context, string, CreateProjectInput) (ProjectRecord, error)
	GetProject(context.Context, string, string) (ProjectRecord, error)
	UpdateProject(context.Context, string, string, UpdateProjectInput) (ProjectRecord, error)
	DeleteProject(context.Context, string, string) error
	RestoreProject(context.Context, string, string) (ProjectRecord, error)
	ListProjectMembers(context.Context, string, string, ListProjectMembersInput) ([]ProjectMembershipRecord, error)
	UpdateProjectMemberRole(context.Context, string, string, string, UpdateProjectMemberRoleInput) (ProjectMembershipRecord, error)
	RemoveProjectMember(context.Context, string, string, string) error
	ListProjectInvitations(context.Context, string, string) ([]ProjectInvitationRecord, error)
	CreateProjectInvitation(context.Context, string, string, CreateProjectInvitationInput) (ProjectInvitationRecord, error)
	CancelProjectInvitation(context.Context, string, string) (ProjectInvitationRecord, error)
	AcceptProjectInvitation(context.Context, AuthenticatedUser, string) (ProjectMembershipRecord, error)
	ListMyProjectInvitations(context.Context, AuthenticatedUser, ListMyProjectInvitationsInput) (ProjectInvitationPage, error)
}

type CurrentUserService interface {
	CurrentUser(context.Context, string) (auth.User, error)
	ValidateCSRF(context.Context, string, string) error
}

type Handlers struct {
	service        ProjectService
	currentUser    CurrentUserService
	allowedOrigins []string
}

type actorUserIDContextKey struct{}
type actorUserContextKey struct{}

func NewHandlers(service ProjectService, currentUser CurrentUserService, allowedOrigins []string) Handlers {
	if service == nil {
		panic("projects: ProjectService is required")
	}
	if currentUser == nil {
		panic("projects: CurrentUserService is required")
	}
	return Handlers{
		service:        service,
		currentUser:    currentUser,
		allowedOrigins: slices.Clone(allowedOrigins),
	}
}

func (h Handlers) Mount(router chi.Router) {
	router.Get("/projects", h.ListProjects)
	router.Get("/projects/{projectId}", h.GetProject)
	router.Get("/projects/{projectId}/members", h.ListProjectMembers)
	router.Get("/projects/{projectId}/invitations", h.ListProjectInvitations)
	router.Get("/project-invitations", h.ListMyProjectInvitations)
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(h.allowedOrigins...))
		router.Use(h.CSRFMiddleware)
		router.Post("/projects", h.CreateProject)
		router.Patch("/projects/{projectId}", h.UpdateProject)
		router.Delete("/projects/{projectId}", h.DeleteProject)
		router.Post("/projects/{projectId}/restore", h.RestoreProject)
		router.Patch("/projects/{projectId}/members/{memberId}", h.UpdateProjectMemberRole)
		router.Delete("/projects/{projectId}/members/{memberId}", h.RemoveProjectMember)
		router.Post("/projects/{projectId}/invitations", h.CreateProjectInvitation)
		router.Post("/project-invitations/{invitationId}/cancel", h.CancelProjectInvitation)
		router.Post("/project-invitations/{invitationId}/accept", h.AcceptProjectInvitation)
	})
}

func (h Handlers) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpapi.RequiresCSRF(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		cookieValue := auth.SessionCookieValue(r)
		user, err := h.currentUser.CurrentUser(r.Context(), cookieValue)
		if err != nil {
			writeProjectServiceError(w, err)
			return
		}
		csrfToken := r.Header.Get("X-CSRF-Token")
		if csrfToken == "" {
			writeProjectProblem(w, http.StatusForbidden, "Forbidden", "missing CSRF token", "csrf.required")
			return
		}
		if err := h.currentUser.ValidateCSRF(r.Context(), cookieValue, csrfToken); err != nil {
			if errors.Is(err, auth.ErrInvalidCSRF) {
				writeProjectProblem(w, http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
				return
			}
			writeProjectProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
			return
		}
		ctx := context.WithValue(r.Context(), actorUserContextKey{}, user)
		ctx = context.WithValue(ctx, actorUserIDContextKey{}, user.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h Handlers) actorUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if actorUserID, ok := r.Context().Value(actorUserIDContextKey{}).(string); ok {
		return actorUserID, true
	}
	user, ok := h.actorUser(w, r)
	if !ok {
		return "", false
	}
	return user.ID, true
}

func (h Handlers) actorUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	if user, ok := r.Context().Value(actorUserContextKey{}).(auth.User); ok {
		return user, true
	}
	user, err := h.currentUser.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if err != nil {
		writeProjectServiceError(w, err)
		return auth.User{}, false
	}
	return user, true
}

type pageMetaResponse struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
}

func writeProjectServiceError(w http.ResponseWriter, err error) {
	if httpapi.WriteQuotaProblem(w, err, time.Now()) {
		return
	}
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeProjectProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, ErrNotFound):
		writeProjectProblem(w, http.StatusNotFound, "Not Found", "project not found", "not_found")
	case errors.Is(err, ErrForbidden):
		writeProjectProblem(w, http.StatusForbidden, "Forbidden", "project access denied", "forbidden")
	case errors.Is(err, ErrValidation):
		writeProjectProblem(w, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	case errors.Is(err, ErrConflict):
		writeProjectProblem(w, http.StatusConflict, "Conflict", "request conflicts with current project state", "conflict")
	default:
		writeProjectProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

func decodeProjectJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(httpapi.JSONBody(r))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid JSON request body")
	}
	return nil
}

func paginationFromRequest(r *http.Request) (int, string, error) {
	query := r.URL.Query()
	cursor := query.Get("cursor")
	limitValue := query.Get("limit")
	if limitValue == "" {
		return defaultPageLimit, cursor, nil
	}
	limit, err := strconv.Atoi(limitValue)
	if err != nil || limit < 1 || limit > maxPageLimit {
		return 0, "", ErrValidation
	}
	return limit, cursor, nil
}

func writeProjectJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeProjectProblem(w http.ResponseWriter, status int, title, detail, code string) {
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(w, "", status, title, detail, code)
}

func formatProjectTime(value time.Time) string {
	return value.Format("2006-01-02T15:04:05.000Z07:00")
}

func authenticatedUser(user auth.User) AuthenticatedUser {
	return AuthenticatedUser{
		ID:            user.ID,
		Email:         user.Email,
		EmailVerified: user.EmailVerified,
	}
}

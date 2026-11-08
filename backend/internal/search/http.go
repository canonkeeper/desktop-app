package search

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/httpapi"

	"github.com/go-chi/chi/v5"
)

type ProjectSearchService interface {
	Search(context.Context, Actor, SearchRequest) (SearchResponse, error)
}

type CurrentUserService interface {
	CurrentUser(context.Context, string) (auth.User, error)
	ValidateCSRF(context.Context, string, string) error
}

type Handlers struct {
	service        ProjectSearchService
	currentUser    CurrentUserService
	allowedOrigins []string
}

type actorUserIDContextKey struct{}

func NewHandlers(service ProjectSearchService, currentUser CurrentUserService, allowedOrigins []string) Handlers {
	if service == nil {
		panic("search: ProjectSearchService is required")
	}
	if currentUser == nil {
		panic("search: CurrentUserService is required")
	}
	return Handlers{
		service:        service,
		currentUser:    currentUser,
		allowedOrigins: slices.Clone(allowedOrigins),
	}
}

func (h Handlers) Mount(router chi.Router) {
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(h.allowedOrigins...))
		router.Use(h.CSRFMiddleware)
		router.Post("/projects/{projectId}/search", h.SearchProject)
	})
}

func (h Handlers) SearchProject(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeSearchProblem(w, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
		return
	}
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	input, err := searchProjectInputFromRequest(r)
	if err != nil {
		writeSearchServiceError(w, err)
		return
	}
	input.ProjectID = chi.URLParam(r, "projectId")
	input, err = normalizeSearchRequest(input)
	if err != nil {
		writeSearchServiceError(w, err)
		return
	}
	response, err := h.service.Search(r.Context(), Actor{UserID: actorUserID}, input)
	if err != nil {
		writeSearchServiceError(w, err)
		return
	}
	writeSearchResponse(w, searchProjectResponseFrom(response))
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
			writeSearchServiceError(w, err)
			return
		}
		csrfToken := r.Header.Get("X-CSRF-Token")
		if csrfToken == "" {
			writeSearchProblem(w, http.StatusForbidden, "Forbidden", "missing CSRF token", "csrf.required")
			return
		}
		if err := h.currentUser.ValidateCSRF(r.Context(), cookieValue, csrfToken); err != nil {
			if errors.Is(err, auth.ErrInvalidCSRF) {
				writeSearchProblem(w, http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
				return
			}
			writeSearchProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
			return
		}
		ctx := context.WithValue(r.Context(), actorUserIDContextKey{}, user.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h Handlers) actorUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if actorUserID, ok := r.Context().Value(actorUserIDContextKey{}).(string); ok {
		return actorUserID, true
	}
	user, err := h.currentUser.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if err != nil {
		writeSearchServiceError(w, err)
		return "", false
	}
	return user.ID, true
}

type searchProjectRequest struct {
	Query      string                `json:"query"`
	Scope      SearchScope           `json:"scope"`
	SourceView governance.SourceView `json:"sourceView"`
	Filters    *SearchProjectFilters `json:"filters"`
	Limit      *int                  `json:"limit"`
	Cursor     *string               `json:"cursor"`
}

func searchProjectInputFromRequest(r *http.Request) (SearchRequest, error) {
	var request searchProjectRequest
	decoder := json.NewDecoder(httpapi.JSONBody(r))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return SearchRequest{}, ErrValidation
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SearchRequest{}, ErrValidation
	}
	limit := defaultSearchLimit
	if request.Limit != nil {
		if *request.Limit < 1 || *request.Limit > maxSearchLimit {
			return SearchRequest{}, ErrValidation
		}
		limit = *request.Limit
	}
	filters := SearchProjectFilters{}
	if request.Filters != nil {
		filters = *request.Filters
	}
	cursor := ""
	if request.Cursor != nil {
		if *request.Cursor == "" || len(*request.Cursor) > searchCursorEncodedLength {
			return SearchRequest{}, ErrValidation
		}
		cursor = *request.Cursor
	}
	return SearchRequest{
		Query: request.Query, Scope: request.Scope, SourceView: request.SourceView,
		Filters: filters, Limit: limit, Cursor: cursor,
	}, nil
}

type searchProjectResponse struct {
	Query        string                `json:"query"`
	Scope        SearchScope           `json:"scope"`
	TextCoverage TextCoverage          `json:"textCoverage"`
	Data         []chapterSearchResult `json:"data"`
	Meta         searchPageMeta        `json:"meta"`
}

type searchPageMeta struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
}

type chapterSearchResult struct {
	ID      string               `json:"id"`
	Kind    SearchResultKind     `json:"kind"`
	Title   string               `json:"title"`
	Excerpt string               `json:"excerpt"`
	Score   float64              `json:"score,omitempty"`
	Locator ChapterSearchLocator `json:"locator"`
}

func searchProjectResponseFrom(response SearchResponse) searchProjectResponse {
	body := searchProjectResponse{
		Query: response.Query, Scope: response.Scope, TextCoverage: response.TextCoverage,
		Data: make([]chapterSearchResult, len(response.Items)),
		Meta: searchPageMeta{HasMore: response.NextCursor != ""},
	}
	if response.NextCursor != "" {
		nextCursor := response.NextCursor
		body.Meta.NextCursor = &nextCursor
	}
	for index, item := range response.Items {
		body.Data[index] = chapterSearchResult{
			ID: item.ID, Kind: SearchResultKindChapter, Title: item.Title, Excerpt: item.Excerpt,
			Score: item.Score, Locator: item.Locator,
		}
	}
	return body
}

func writeSearchResponse(w http.ResponseWriter, response searchProjectResponse) {
	w.Header().Set("Cache-Control", "no-store")
	writeSearchJSON(w, http.StatusOK, response)
}

func writeSearchServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		writeSearchProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, ErrNotFound):
		writeSearchProblem(w, http.StatusNotFound, "Not Found", "search resource not found", "not_found")
	case errors.Is(err, ErrForbidden):
		writeSearchProblem(w, http.StatusForbidden, "Forbidden", "search access denied", "forbidden")
	case errors.Is(err, ErrValidation):
		writeSearchProblem(w, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	default:
		writeSearchProblem(w, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

func writeSearchJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeSearchProblem(w http.ResponseWriter, status int, title, detail, code string) {
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(w, "", status, title, detail, code)
}

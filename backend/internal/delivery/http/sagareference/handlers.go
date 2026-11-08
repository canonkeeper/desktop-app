// Package sagareference delivers the saga map (specs/2026-11-04-saga-map.md) — getSagaMap, getSagaMapNode,
// searchSagaMap and what the author takes off the map — and the shared saga reads listSagaGlossary and
// getSagaContents over HTTP.
package sagareference

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"
	service "canonkeeper/backend/internal/services/sagareference"

	"github.com/go-chi/chi/v5"
)

type CurrentUser interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}

// MapHiddenUseCases are the decisions of the author about the map (R11.3).
type MapHiddenUseCases struct {
	List    *service.ListMapHidden
	Hide    *service.HideOnMap
	Restore *service.RestoreOnMap
}

type Handlers struct {
	glossary    *service.ListGlossary
	sagaMap     *service.GetMap
	node        *service.GetMapNode
	search      *service.SearchMap
	contents    *service.GetContents
	hidden      MapHiddenUseCases
	currentUser CurrentUser
	origins     []string
}

func NewHandlers(glossary *service.ListGlossary, sagaMap *service.GetMap, node *service.GetMapNode, search *service.SearchMap,
	contents *service.GetContents, hidden MapHiddenUseCases, currentUser CurrentUser, allowedOrigins []string) Handlers {
	if glossary == nil || sagaMap == nil || node == nil || search == nil || contents == nil || hidden.List == nil || hidden.Hide == nil || hidden.Restore == nil || currentUser == nil {
		panic("saga reference HTTP dependencies are required")
	}
	return Handlers{glossary: glossary, sagaMap: sagaMap, node: node, search: search, contents: contents, hidden: hidden, currentUser: currentUser, origins: slices.Clone(allowedOrigins)}
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Get("/projects/{projectId}/saga/glossary", handlers.Glossary)
	router.Get("/projects/{projectId}/saga/map", handlers.Map)
	router.Get("/projects/{projectId}/saga/map/node", handlers.MapNode)
	router.Get("/projects/{projectId}/saga/map/search", handlers.MapSearch)
	router.Get("/projects/{projectId}/saga/contents", handlers.Contents)
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(handlers.origins...))
		router.Get("/projects/{projectId}/saga/map/hidden", handlers.MapHidden)
		router.Post("/projects/{projectId}/saga/map/hidden", handlers.HideOnMap)
		router.Delete("/projects/{projectId}/saga/map/hidden/{hiddenId}", handlers.RestoreOnMap)
	})
}

func (handlers Handlers) Glossary(w http.ResponseWriter, request *http.Request) {
	scope, query, err := handlers.scope(request, "type", "q", "offset", "limit")
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	offset, err := optionalInt(query, "offset")
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	limit, err := optionalInt(query, "limit")
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	result, err := handlers.glossary.Execute(request.Context(), service.GlossaryInput{
		Scope: scope, Type: query["type"], Query: query["q"], Offset: offset, Limit: limit,
	})
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	writeJSON(w, glossaryPageDTO(result))
}

// mapCacheControl lets the browser keep the map and ask again with its revision (R1.5).
const mapCacheControl = "private, no-cache"

// Map serves getSagaMap; a browser holding the current revision gets 304 without a body.
func (handlers Handlers) Map(w http.ResponseWriter, request *http.Request) {
	scope, _, err := handlers.scope(request)
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	result, err := handlers.sagaMap.Execute(request.Context(), service.MapInput{Scope: scope, Known: entityTags(request.Header.Get("If-None-Match"))})
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	if result.Revision == "" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("ETag", `"`+result.Revision+`"`)
		w.Header().Set("Cache-Control", mapCacheControl)
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if result.NotModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeStored(w, request, result.Document)
}

// writeStored sends a prepared document, gzip of JSON: as it is to a client that takes gzip, unpacked to any other.
func writeStored(w http.ResponseWriter, request *http.Request, document []byte) {
	w.Header().Set("Content-Type", "application/json")
	if acceptsGzip(request.Header.Get("Accept-Encoding")) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(document)
		return
	}
	reader, err := gzip.NewReader(bytes.NewReader(document))
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	defer reader.Close()
	_, _ = io.Copy(w, reader)
}

// acceptsGzip reports whether an Accept-Encoding header takes gzip: listed without q=0.
func acceptsGzip(header string) bool {
	for _, coding := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(coding), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			return strings.ReplaceAll(strings.TrimSpace(params), " ", "") != "q=0"
		}
	}
	return false
}

// entityTags reads the revisions of an If-None-Match header: «"a", W/"b"» gives a and b.
func entityTags(header string) []string {
	var tags []string
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimPrefix(strings.TrimSpace(tag), "W/")
		if len(tag) >= 2 && strings.HasPrefix(tag, `"`) && strings.HasSuffix(tag, `"`) {
			tags = append(tags, tag[1:len(tag)-1])
		}
	}
	return tags
}

func (handlers Handlers) MapNode(w http.ResponseWriter, request *http.Request) {
	scope, query, err := handlers.scope(request, "key", "to")
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	result, err := handlers.node.Execute(request.Context(), service.MapNodeInput{Scope: scope, Key: query["key"], To: query["to"]})
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Accept-Encoding")
	writeStored(w, request, result.Document)
}

// MapSearch serves searchSagaMap.
func (handlers Handlers) MapSearch(w http.ResponseWriter, request *http.Request) {
	scope, query, err := handlers.scope(request, "q", "limit")
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	limit, err := optionalInt(query, "limit")
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	entries, err := handlers.search.Execute(request.Context(), service.MapSearchInput{Scope: scope, Query: query["q"], Limit: limit})
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	writeJSON(w, map[string]any{"entries": entries})
}

func (handlers Handlers) Contents(w http.ResponseWriter, request *http.Request) {
	scope, query, err := handlers.scope(request, "bookId", "q")
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	result, err := handlers.contents.Execute(request.Context(), service.ContentsInput{
		Scope: scope, BookID: query["bookId"], Query: query["q"],
	})
	if err != nil {
		writeError(w, request.Context(), err)
		return
	}
	writeJSON(w, contentsDTO(result))
}

// scope authenticates the actor and reads sourceView and the allowed single-valued parameters.
func (handlers Handlers) scope(request *http.Request, allowed ...string) (service.Scope, map[string]string, error) {
	user, err := handlers.currentUser.CurrentUser(request.Context(), auth.SessionCookieValue(request))
	if err != nil {
		return service.Scope{}, nil, err
	}
	known := map[string]bool{"sourceView": true}
	for _, name := range allowed {
		known[name] = true
	}
	values := map[string]string{}
	for name, list := range request.URL.Query() {
		if !known[name] || len(list) != 1 {
			return service.Scope{}, nil, service.ErrInvalid
		}
		values[name] = list[0]
	}
	view := values["sourceView"]
	delete(values, "sourceView")
	return service.Scope{ActorUserID: user.ID, ProjectID: strings.TrimSpace(chi.URLParam(request, "projectId")), View: view}, values, nil
}

func optionalInt(query map[string]string, name string) (int, error) {
	value, ok := query[name]
	if !ok {
		return 0, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, service.ErrInvalid
	}
	return number, nil
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, ctx context.Context, err error) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, service.ErrNotFound):
		httpapi.WriteProblem(w, "", http.StatusNotFound, "Not Found", "saga reference not found", "not_found")
	case errors.Is(err, service.ErrDenied):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "saga reference access denied", "forbidden")
	case errors.Is(err, service.ErrInvalid):
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	case errors.Is(err, sagamemory.ErrSearchNotReady):
		// The search rows of the saga are rebuilt after a restore; the page works again then.
		httpapi.WriteProblem(w, "", http.StatusServiceUnavailable, "Service Unavailable", "saga search rows are not built yet", "saga.not_ready")
	default:
		observability.ReportUnexpectedError(ctx, err)
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

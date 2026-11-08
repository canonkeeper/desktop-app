// Package authormaterials delivers the materials of the author over HTTP (specs/2026-11-04-saga-map.md, UC9): the list and
// the import for the members of a project; changes, removal and the import itself for an editor or above, each behind the
// session's write proof.
package authormaterials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"
	service "canonkeeper/backend/internal/services/authormaterials"

	"github.com/go-chi/chi/v5"
)

const (
	// maxBody bounds a JSON request: one record, or the answer to an import of at most 5000 rows.
	maxBody = 1 << 20
	// maxUploadBody bounds an import: the file or the text with their headers.
	maxUploadBody = domain.MaxFileBytes + 1<<20
)

type CurrentUser interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}

// UseCases are the operations of the materials.
type UseCases struct {
	List      service.List
	Update    service.Update
	Delete    service.Delete
	Start     service.Start
	GetImport service.GetImport
	Confirm   service.Confirm
	Discard   service.Discard
}

type Handlers struct {
	useCases       UseCases
	currentUser    CurrentUser
	allowedOrigins []string
}

func NewHandlers(useCases UseCases, currentUser CurrentUser, allowedOrigins []string) Handlers {
	if currentUser == nil {
		panic("author materials HTTP: dependencies are required")
	}
	return Handlers{useCases: useCases, currentUser: currentUser, allowedOrigins: slices.Clone(allowedOrigins)}
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(handlers.allowedOrigins...))
		router.Get("/projects/{projectId}/author-materials", handlers.list)
		router.Put("/projects/{projectId}/author-materials/{materialId}", handlers.update)
		router.Delete("/projects/{projectId}/author-materials/{materialId}", handlers.delete)
		router.Post("/projects/{projectId}/author-material-imports", handlers.start)
		router.Get("/projects/{projectId}/author-material-imports/current", handlers.current)
		router.Post("/projects/{projectId}/author-material-imports/{importId}/confirm", handlers.confirm)
		router.Delete("/projects/{projectId}/author-material-imports/{importId}", handlers.discard)
	})
}

func projectID(r *http.Request) string { return strings.TrimSpace(chi.URLParam(r, "projectId")) }

func (handlers Handlers) reader(w http.ResponseWriter, r *http.Request) (service.Actor, bool) {
	user, err := handlers.currentUser.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if err != nil {
		writeError(w, r.Context(), err)
		return service.Actor{}, false
	}
	return service.Actor{UserID: user.ID, ProjectID: projectID(r)}, true
}

// writer checks the write proof of the session; the role is checked in the transaction of the write.
func (handlers Handlers) writer(w http.ResponseWriter, r *http.Request) (service.Write, bool) {
	user, proof, err := handlers.currentUser.AuthenticateWrite(r.Context(), auth.SessionCookieValue(r), r.Header.Get("X-CSRF-Token"))
	if err != nil {
		writeError(w, r.Context(), err)
		return service.Write{}, false
	}
	return service.Write{Actor: service.Actor{UserID: user.ID, ProjectID: projectID(r)}, Proof: proof}, true
}

func (handlers Handlers) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.reader(w, r)
	if !ok {
		return
	}
	query := map[string]string{}
	for name, list := range r.URL.Query() {
		if !slices.Contains([]string{"kind", "q", "cursor", "limit"}, name) || len(list) != 1 {
			writeError(w, r.Context(), domain.ErrInvalid)
			return
		}
		query[name] = list[0]
	}
	input := service.ListInput{Kind: query["kind"], Query: query["q"], Cursor: query["cursor"]}
	if value, present := query["limit"]; present {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 {
			writeError(w, r.Context(), domain.ErrInvalid)
			return
		}
		input.Limit = limit
	}
	found, err := handlers.useCases.List.Execute(r.Context(), actor, input)
	if err != nil {
		writeError(w, r.Context(), err)
		return
	}
	writeJSON(w, http.StatusOK, pageDTO(found))
}

func readJSON(w http.ResponseWriter, r *http.Request, body any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil || !utf8.Valid(raw) {
		return domain.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(body) != nil {
		return domain.ErrInvalid
	}
	return nil
}

type updateBody struct {
	Name             string            `json:"name"`
	Kind             string            `json:"kind"`
	Description      string            `json:"description"`
	Properties       []domain.Property `json:"properties"`
	Section          string            `json:"section"`
	ExpectedRevision int64             `json:"expectedRevision"`
}

func (handlers Handlers) update(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	var body updateBody
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, r.Context(), err)
		return
	}
	updated, err := handlers.useCases.Update.Execute(r.Context(), actor, chi.URLParam(r, "materialId"), body.ExpectedRevision, domain.Draft{
		Name: body.Name, Kind: domain.Kind(body.Kind), Description: body.Description, Properties: body.Properties, Section: body.Section,
	})
	if err != nil {
		writeError(w, r.Context(), err)
		return
	}
	writeJSON(w, http.StatusOK, recordDTO(updated))
}

func (handlers Handlers) delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()["expectedRevision"]
	if len(values) != 1 || len(r.URL.Query()) != 1 {
		writeError(w, r.Context(), domain.ErrInvalid)
		return
	}
	revision, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		writeError(w, r.Context(), domain.ErrInvalid)
		return
	}
	if err := handlers.useCases.Delete.Execute(r.Context(), actor, chi.URLParam(r, "materialId"), revision); err != nil {
		writeError(w, r.Context(), err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func requestPath(w http.ResponseWriter, r *http.Request) (service.RequestPath, bool) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		writeError(w, r.Context(), domain.ErrInvalid)
		return service.RequestPath{}, false
	}
	return service.RequestPath{Key: keys[0], Method: r.Method, Path: r.URL.Path}, true
}

func (handlers Handlers) start(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	request, ok := requestPath(w, r)
	if !ok {
		return
	}
	input, err := decodeStart(w, r)
	if err != nil {
		writeError(w, r.Context(), err)
		return
	}
	started, err := handlers.useCases.Start.Execute(r.Context(), actor, input, request)
	if err != nil {
		writeError(w, r.Context(), err)
		return
	}
	writeJSON(w, http.StatusAccepted, importDTO(&service.ImportView{Import: started}))
}

// decodeStart reads one file part or one text part; anything else is a bad request.
func decodeStart(w http.ResponseWriter, r *http.Request) (service.StartInput, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return service.StartInput{}, domain.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)
	reader, err := r.MultipartReader()
	if err != nil {
		return service.StartInput{}, domain.ErrInvalid
	}
	var input service.StartInput
	parts := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return service.StartInput{}, readError(err)
		}
		if parts++; parts > 1 {
			return service.StartInput{}, domain.ErrInvalid
		}
		switch name := part.FormName(); {
		case name == "file" && part.FileName() != "":
			input.FileName = part.FileName()
			input.Content, err = io.ReadAll(io.LimitReader(part, domain.MaxFileBytes+1))
		case name == "text" && part.FileName() == "":
			var raw []byte
			raw, err = io.ReadAll(io.LimitReader(part, domain.MaxFileBytes+1))
			if err == nil && !utf8.Valid(raw) {
				return service.StartInput{}, domain.ErrInvalid
			}
			input.Text = string(raw)
		default:
			return service.StartInput{}, domain.ErrInvalid
		}
		if err != nil {
			return service.StartInput{}, readError(err)
		}
	}
	return input, nil
}

func readError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return domain.ErrTooLarge
	}
	return domain.ErrInvalid
}

func (handlers Handlers) current(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.reader(w, r)
	if !ok {
		return
	}
	found, err := handlers.useCases.GetImport.Execute(r.Context(), actor)
	if err != nil {
		writeError(w, r.Context(), err)
		return
	}
	writeJSON(w, http.StatusOK, importState{Import: importDTO(found)})
}

type choiceBody struct {
	Index       int                `json:"index"`
	Name        *string            `json:"name"`
	Kind        *string            `json:"kind"`
	Description *string            `json:"description"`
	Properties  *[]domain.Property `json:"properties"`
}

type confirmBody struct {
	Items []choiceBody `json:"items"`
}

func (handlers Handlers) confirm(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	request, ok := requestPath(w, r)
	if !ok {
		return
	}
	var body confirmBody
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, r.Context(), err)
		return
	}
	choices := make([]domain.Choice, len(body.Items))
	for index, item := range body.Items {
		choices[index] = domain.Choice{Index: item.Index, Name: item.Name, Description: item.Description, Properties: item.Properties}
		if item.Kind != nil {
			kind := domain.Kind(*item.Kind)
			choices[index].Kind = &kind
		}
	}
	saved, err := handlers.useCases.Confirm.Execute(r.Context(), actor, chi.URLParam(r, "importId"), choices, request)
	if err != nil {
		writeError(w, r.Context(), err)
		return
	}
	writeJSON(w, http.StatusOK, confirmResult{Saved: saved})
}

func (handlers Handlers) discard(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	if err := handlers.useCases.Discard.Execute(r.Context(), actor, chi.URLParam(r, "importId")); err != nil {
		writeError(w, r.Context(), err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// The JSON shapes of contracts/openapi.json (AuthorMaterial* schemas).

type record struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Description string            `json:"description"`
	Properties  []domain.Property `json:"properties"`
	Section     string            `json:"section"`
	SourceName  string            `json:"sourceName"`
	SourceKind  string            `json:"sourceKind"`
	Revision    int64             `json:"revision"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type pageMeta struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
	TotalCount int64   `json:"totalCount"`
}

type page struct {
	Data []record `json:"data"`
	Meta pageMeta `json:"meta"`
}

type candidate struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Description string            `json:"description"`
	Properties  []domain.Property `json:"properties"`
	Section     string            `json:"section"`
	Match       string            `json:"match"`
	BookName    string            `json:"bookName,omitempty"`
}

type importBody struct {
	ID         string      `json:"id"`
	JobID      string      `json:"jobId"`
	JobStatus  string      `json:"jobStatus"`
	SourceName string      `json:"sourceName"`
	SourceKind string      `json:"sourceKind"`
	CreatedAt  time.Time   `json:"createdAt"`
	Candidates []candidate `json:"candidates"`
}

type importState struct {
	Import *importBody `json:"import"`
}

type confirmResult struct {
	Saved int `json:"saved"`
}

func properties(list []domain.Property) []domain.Property {
	if list == nil {
		return []domain.Property{}
	}
	return list
}

func recordDTO(item domain.Record) record {
	return record{
		ID: item.ID, Name: item.Name, Kind: string(item.Kind), Description: item.Description, Properties: properties(item.Properties),
		Section: item.Section, SourceName: item.SourceName, SourceKind: string(item.SourceKind), Revision: item.Revision,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func pageDTO(found domain.Page) page {
	result := page{Data: make([]record, len(found.Records)), Meta: pageMeta{TotalCount: found.Total}}
	for index, item := range found.Records {
		result.Data[index] = recordDTO(item)
	}
	if found.NextKey != "" {
		next := found.NextKey
		result.Meta.HasMore, result.Meta.NextCursor = true, &next
	}
	return result
}

func importDTO(view *service.ImportView) *importBody {
	if view == nil {
		return nil
	}
	body := &importBody{
		ID: view.ID, JobID: view.JobID, JobStatus: view.JobStatus, SourceName: view.SourceName, SourceKind: string(view.SourceKind), CreatedAt: view.CreatedAt,
	}
	if view.Views != nil {
		body.Candidates = make([]candidate, len(view.Views))
		for index, item := range view.Views {
			body.Candidates[index] = candidate{
				Name: item.Name, Kind: string(item.Kind), Description: item.Description, Properties: properties(item.Properties),
				Section: item.Section, Match: string(item.Match), BookName: item.BookName,
			}
		}
	}
	return body
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, ctx context.Context, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if httpapi.WriteQuotaProblem(w, err, time.Now()) {
		return
	}
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, auth.ErrInvalidCSRF):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
	case errors.Is(err, domain.ErrForbidden):
		httpapi.WriteProblem(w, "", http.StatusForbidden, "Forbidden", "the materials are changed by an editor", "forbidden")
	case errors.Is(err, domain.ErrNotFound):
		httpapi.WriteProblem(w, "", http.StatusNotFound, "Not Found", "not found", "not_found")
	case errors.Is(err, domain.ErrImportBusy):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "an import of the saga is running", "author_materials.import_busy")
	case errors.Is(err, domain.ErrConflict):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the record changed or its name is taken", "conflict")
	case errors.Is(err, domain.ErrTooLarge):
		httpapi.WriteProblem(w, "", http.StatusRequestEntityTooLarge, "Payload Too Large", "the file is over the limits of an import", "author_materials.too_large")
	case errors.Is(err, domain.ErrUnreadable):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the file is not a readable XLSX or DOCX", "author_materials.unreadable")
	case errors.Is(err, domain.ErrEmpty):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the file holds nothing to read", "author_materials.empty")
	case errors.Is(err, domain.ErrProtected):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the file is under a password", "author_materials.protected")
	case errors.Is(err, domain.ErrOldFormat):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the file is of an old Office format: XLS, DOC or RTF", "author_materials.old_format")
	case errors.Is(err, domain.ErrOpenDocument):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the file is of LibreOffice: ODS or ODT", "author_materials.open_document")
	case errors.Is(err, domain.ErrPDF):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the file is a PDF", "author_materials.pdf")
	case errors.Is(err, domain.ErrPicture):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the file is a picture", "author_materials.picture")
	case errors.Is(err, domain.ErrPlainText):
		httpapi.WriteProblem(w, "", http.StatusUnprocessableEntity, "Unprocessable Entity", "the file is plain text: CSV or TXT", "author_materials.plain_text")
	case errors.Is(err, domain.ErrInvalid):
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	default:
		observability.ReportUnexpectedError(ctx, err)
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

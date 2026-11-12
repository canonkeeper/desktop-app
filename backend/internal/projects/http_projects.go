package projects

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h Handlers) ListProjects(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	input, err := listProjectsInputFromRequest(r)
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	page, err := h.service.ListProjects(r.Context(), actorUserID, input)
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	writeProjectListResponse(w, http.StatusOK, page, input.State)
}

func (h Handlers) CreateProject(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	var request createProjectRequest
	if err := decodeProjectJSON(r, &request); err != nil {
		writeProjectProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON request body", "request.invalid")
		return
	}
	input, err := request.toServiceInput()
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	project, err := h.service.CreateProject(r.Context(), actorUserID, input)
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	WriteProjectResponse(w, http.StatusCreated, project)
}

func (h Handlers) GetProject(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	project, err := h.service.GetProject(r.Context(), actorUserID, chi.URLParam(r, "projectId"))
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	WriteProjectResponse(w, http.StatusOK, project)
}

func (h Handlers) UpdateProject(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	var request updateProjectRequest
	if err := decodeProjectJSON(r, &request); err != nil {
		writeProjectProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON request body", "request.invalid")
		return
	}
	input, err := request.toServiceInput()
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	project, err := h.service.UpdateProject(r.Context(), actorUserID, chi.URLParam(r, "projectId"), input)
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	WriteProjectResponse(w, http.StatusOK, project)
}

func (h Handlers) DeleteProject(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	if err := h.service.DeleteProject(r.Context(), actorUserID, chi.URLParam(r, "projectId")); err != nil {
		writeProjectServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h Handlers) RestoreProject(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	project, err := h.service.RestoreProject(r.Context(), actorUserID, chi.URLParam(r, "projectId"))
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	WriteProjectResponse(w, http.StatusOK, project)
}

type createProjectRequest struct {
	Title       optionalString `json:"title"`
	Description optionalString `json:"description"`
}

func (r createProjectRequest) toServiceInput() (CreateProjectInput, error) {
	if !r.Title.Set || r.Title.Null {
		return CreateProjectInput{}, ErrValidation
	}
	input := CreateProjectInput{Title: r.Title.Value}
	if r.Description.Set {
		if r.Description.Null {
			return CreateProjectInput{}, ErrValidation
		}
		input.Description = r.Description.Value
	}
	return input, nil
}

type updateProjectRequest struct {
	Title       optionalString `json:"title"`
	Description optionalString `json:"description"`
}

func (r updateProjectRequest) toServiceInput() (UpdateProjectInput, error) {
	var input UpdateProjectInput
	if r.Title.Set {
		if r.Title.Null {
			return UpdateProjectInput{}, ErrValidation
		}
		input.Title = &r.Title.Value
	}
	if r.Description.Set {
		input.DescriptionSet = true
		if !r.Description.Null {
			input.Description = &r.Description.Value
		}
	}
	return input, nil
}

type optionalString struct {
	Set   bool
	Null  bool
	Value string
}

func (s *optionalString) UnmarshalJSON(data []byte) error {
	s.Set = true
	if string(data) == "null" {
		s.Null = true
		s.Value = ""
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	s.Null = false
	s.Value = value
	return nil
}

type projectResponse struct {
	ID                string                    `json:"id"`
	Title             string                    `json:"title"`
	Description       string                    `json:"description"`
	Demo              bool                      `json:"demo,omitempty"`
	BookCount         int                       `json:"bookCount"`
	ChapterCount      int                       `json:"chapterCount"`
	WordCount         int                       `json:"wordCount"`
	CreatedAt         string                    `json:"createdAt"`
	UpdatedAt         string                    `json:"updatedAt"`
	DeletedAt         *string                   `json:"deletedAt,omitempty"`
	RestoreUntil      *string                   `json:"restoreUntil,omitempty"`
	CurrentMembership projectMembershipResponse `json:"currentMembership"`
}

type projectListResponse struct {
	Data []projectResponse `json:"data"`
	Meta pageMetaResponse  `json:"meta"`
}

type deletedProjectResponse struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	DeletedAt    *string `json:"deletedAt"`
	RestoreUntil *string `json:"restoreUntil"`
}

type deletedProjectListResponse struct {
	Data []deletedProjectResponse `json:"data"`
	Meta pageMetaResponse         `json:"meta"`
}

func projectResponseFromRecord(project ProjectRecord) projectResponse {
	response := projectResponse{
		ID:                project.ID,
		Title:             project.Title,
		Description:       project.Description,
		Demo:              project.Demo,
		BookCount:         project.BookCount,
		ChapterCount:      project.ChapterCount,
		WordCount:         project.WordCount,
		CreatedAt:         formatProjectTime(project.CreatedAt),
		UpdatedAt:         formatProjectTime(project.UpdatedAt),
		CurrentMembership: projectMembershipResponseFromRecord(project.CurrentMembership),
	}
	if project.DeletedAt != nil {
		deletedAt := formatProjectTime(*project.DeletedAt)
		response.DeletedAt = &deletedAt
	}
	if project.RestoreUntil != nil {
		restoreUntil := formatProjectTime(*project.RestoreUntil)
		response.RestoreUntil = &restoreUntil
	}
	return response
}

func WriteProjectResponse(w http.ResponseWriter, status int, project ProjectRecord) {
	w.Header().Set("Cache-Control", "no-store")
	writeProjectJSON(w, status, projectResponseFromRecord(project))
}

func writeProjectListResponse(w http.ResponseWriter, status int, page ProjectPage, state ProjectListState) {
	if state == ProjectListDeleted {
		data := make([]deletedProjectResponse, len(page.Data))
		for i, project := range page.Data {
			response := deletedProjectResponse{ID: project.ID, Title: project.Title}
			if project.DeletedAt != nil {
				deletedAt := formatProjectTime(*project.DeletedAt)
				response.DeletedAt = &deletedAt
			}
			if project.RestoreUntil != nil {
				restoreUntil := formatProjectTime(*project.RestoreUntil)
				response.RestoreUntil = &restoreUntil
			}
			data[i] = response
		}
		w.Header().Set("Cache-Control", "no-store")
		writeProjectJSON(w, status, deletedProjectListResponse{
			Data: data,
			Meta: pageMetaResponse{HasMore: page.Meta.HasMore, NextCursor: page.Meta.NextCursor},
		})
		return
	}
	data := make([]projectResponse, len(page.Data))
	for i, project := range page.Data {
		data[i] = projectResponseFromRecord(project)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeProjectJSON(w, status, projectListResponse{
		Data: data,
		Meta: pageMetaResponse{HasMore: page.Meta.HasMore, NextCursor: page.Meta.NextCursor},
	})
}

func listProjectsInputFromRequest(r *http.Request) (ListProjectsInput, error) {
	limit, cursor, err := paginationFromRequest(r)
	if err != nil {
		return ListProjectsInput{}, err
	}
	state := ProjectListState(r.URL.Query().Get("state"))
	if state != "" && state != ProjectListActive && state != ProjectListDeleted {
		return ListProjectsInput{}, ErrValidation
	}
	return ListProjectsInput{Limit: limit, Cursor: cursor, State: state}, nil
}

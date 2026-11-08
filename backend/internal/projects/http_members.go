package projects

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h Handlers) ListProjectMembers(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	input, err := listProjectMembersInputFromRequest(r)
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	members, err := h.service.ListProjectMembers(r.Context(), actorUserID, chi.URLParam(r, "projectId"), input)
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	writeProjectMembershipListResponse(w, http.StatusOK, members)
}

func (h Handlers) UpdateProjectMemberRole(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	var request updateProjectMemberRoleRequest
	if err := decodeProjectJSON(r, &request); err != nil {
		writeProjectProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON request body", "request.invalid")
		return
	}
	membership, err := h.service.UpdateProjectMemberRole(
		r.Context(),
		actorUserID,
		chi.URLParam(r, "projectId"),
		chi.URLParam(r, "memberId"),
		request.toServiceInput(),
	)
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	writeProjectMembershipResponse(w, http.StatusOK, membership)
}

func (h Handlers) RemoveProjectMember(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	if err := h.service.RemoveProjectMember(r.Context(), actorUserID, chi.URLParam(r, "projectId"), chi.URLParam(r, "memberId")); err != nil {
		writeProjectServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

type updateProjectMemberRoleRequest struct {
	Role Role `json:"role"`
}

func (r updateProjectMemberRoleRequest) toServiceInput() UpdateProjectMemberRoleInput {
	return UpdateProjectMemberRoleInput{Role: r.Role}
}

type projectMembershipResponse struct {
	ID          string           `json:"id"`
	ProjectID   string           `json:"projectId"`
	UserID      string           `json:"userId"`
	Email       string           `json:"email,omitempty"`
	DisplayName *string          `json:"displayName,omitempty"`
	Role        Role             `json:"role"`
	Status      MembershipStatus `json:"status"`
	CreatedAt   string           `json:"createdAt"`
	UpdatedAt   string           `json:"updatedAt"`
}

type projectMembershipListResponse struct {
	Data []projectMembershipResponse `json:"data"`
	Meta pageMetaResponse            `json:"meta"`
}

func projectMembershipResponseFromRecord(membership ProjectMembershipRecord) projectMembershipResponse {
	return projectMembershipResponse{
		ID:          membership.ID,
		ProjectID:   membership.ProjectID,
		UserID:      membership.UserID,
		Email:       membership.Email,
		DisplayName: membership.DisplayName,
		Role:        membership.Role,
		Status:      membership.Status,
		CreatedAt:   formatProjectTime(membership.CreatedAt),
		UpdatedAt:   formatProjectTime(membership.UpdatedAt),
	}
}

func writeProjectMembershipResponse(w http.ResponseWriter, status int, membership ProjectMembershipRecord) {
	w.Header().Set("Cache-Control", "no-store")
	writeProjectJSON(w, status, projectMembershipResponseFromRecord(membership))
}

func writeProjectMembershipListResponse(w http.ResponseWriter, status int, memberships []ProjectMembershipRecord) {
	data := make([]projectMembershipResponse, len(memberships))
	for i, membership := range memberships {
		data[i] = projectMembershipResponseFromRecord(membership)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeProjectJSON(w, status, projectMembershipListResponse{
		Data: data,
		Meta: pageMetaResponse{HasMore: false, NextCursor: nil},
	})
}

func listProjectMembersInputFromRequest(r *http.Request) (ListProjectMembersInput, error) {
	limit, cursor, err := paginationFromRequest(r)
	if err != nil {
		return ListProjectMembersInput{}, err
	}
	return ListProjectMembersInput{Limit: limit, Cursor: cursor}, nil
}

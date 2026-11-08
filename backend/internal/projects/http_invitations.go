package projects

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h Handlers) ListProjectInvitations(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	invitations, err := h.service.ListProjectInvitations(r.Context(), actorUserID, chi.URLParam(r, "projectId"))
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	writeProjectInvitationListResponse(w, http.StatusOK, invitations)
}

func (h Handlers) CreateProjectInvitation(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	var request createProjectInvitationRequest
	if err := decodeProjectJSON(r, &request); err != nil {
		writeProjectProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON request body", "request.invalid")
		return
	}
	invitation, err := h.service.CreateProjectInvitation(r.Context(), actorUserID, chi.URLParam(r, "projectId"), request.toServiceInput())
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	writeProjectInvitationResponse(w, http.StatusCreated, invitation)
}

func (h Handlers) CancelProjectInvitation(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	invitation, err := h.service.CancelProjectInvitation(r.Context(), actorUserID, chi.URLParam(r, "invitationId"))
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	writeProjectInvitationResponse(w, http.StatusOK, invitation)
}

func (h Handlers) AcceptProjectInvitation(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorUser(w, r)
	if !ok {
		return
	}
	membership, err := h.service.AcceptProjectInvitation(r.Context(), authenticatedUser(actor), chi.URLParam(r, "invitationId"))
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	writeProjectMembershipResponse(w, http.StatusOK, membership)
}

func (h Handlers) ListMyProjectInvitations(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorUser(w, r)
	if !ok {
		return
	}
	limit, cursor, err := paginationFromRequest(r)
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	page, err := h.service.ListMyProjectInvitations(r.Context(), authenticatedUser(actor), ListMyProjectInvitationsInput{
		Limit:  limit,
		Cursor: cursor,
	})
	if err != nil {
		writeProjectServiceError(w, err)
		return
	}
	writeProjectInvitationPageResponse(w, http.StatusOK, page)
}

type createProjectInvitationRequest struct {
	Email string `json:"email"`
	Role  Role   `json:"role"`
}

func (r createProjectInvitationRequest) toServiceInput() CreateProjectInvitationInput {
	return CreateProjectInvitationInput{
		Email: r.Email,
		Role:  r.Role,
	}
}

type projectInvitationProjectResponse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type projectInvitationInviterResponse struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

type projectInvitationResponse struct {
	ID         string                           `json:"id"`
	ProjectID  string                           `json:"projectId"`
	Project    projectInvitationProjectResponse `json:"project"`
	Inviter    projectInvitationInviterResponse `json:"inviter"`
	Email      string                           `json:"email"`
	Role       Role                             `json:"role"`
	Status     ProjectInvitationStatus          `json:"status"`
	ExpiresAt  string                           `json:"expiresAt"`
	AcceptedAt *string                          `json:"acceptedAt,omitempty"`
	CreatedAt  string                           `json:"createdAt"`
	UpdatedAt  string                           `json:"updatedAt"`
}

type projectInvitationListResponse struct {
	Data []projectInvitationResponse `json:"data"`
	Meta pageMetaResponse            `json:"meta"`
}

func projectInvitationResponseFromRecord(invitation ProjectInvitationRecord) projectInvitationResponse {
	var acceptedAt *string
	if invitation.AcceptedAt != nil {
		formatted := formatProjectTime(*invitation.AcceptedAt)
		acceptedAt = &formatted
	}
	return projectInvitationResponse{
		ID:        invitation.ID,
		ProjectID: invitation.ProjectID,
		Project: projectInvitationProjectResponse{
			ID:    invitation.ProjectID,
			Title: invitation.ProjectTitle,
		},
		Inviter: projectInvitationInviterResponse{
			DisplayName: invitation.InviterDisplayName,
			Email:       invitation.InviterEmail,
		},
		Email:      invitation.Email,
		Role:       invitation.Role,
		Status:     invitation.Status,
		ExpiresAt:  formatProjectTime(invitation.ExpiresAt),
		AcceptedAt: acceptedAt,
		CreatedAt:  formatProjectTime(invitation.CreatedAt),
		UpdatedAt:  formatProjectTime(invitation.UpdatedAt),
	}
}

func writeProjectInvitationResponse(w http.ResponseWriter, status int, invitation ProjectInvitationRecord) {
	w.Header().Set("Cache-Control", "no-store")
	writeProjectJSON(w, status, projectInvitationResponseFromRecord(invitation))
}

func writeProjectInvitationListResponse(w http.ResponseWriter, status int, invitations []ProjectInvitationRecord) {
	writeProjectInvitationPageResponse(w, status, ProjectInvitationPage{Data: invitations})
}

func writeProjectInvitationPageResponse(w http.ResponseWriter, status int, page ProjectInvitationPage) {
	invitations := page.Data
	data := make([]projectInvitationResponse, len(invitations))
	for i, invitation := range invitations {
		data[i] = projectInvitationResponseFromRecord(invitation)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeProjectJSON(w, status, projectInvitationListResponse{
		Data: data,
		Meta: pageMetaResponse{HasMore: page.Meta.HasMore, NextCursor: page.Meta.NextCursor},
	})
}

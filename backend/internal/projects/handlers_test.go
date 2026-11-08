package projects

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"github.com/go-chi/chi/v5"
)

func TestGetProjectNonMemberReturns404(t *testing.T) {
	projectService := &fakeProjectService{
		getProject: func(_ context.Context, actorUserID, projectID string) (ProjectRecord, error) {
			if actorUserID != "user-1" {
				t.Fatalf("actorUserID = %q, want user-1", actorUserID)
			}
			if projectID != "project-1" {
				t.Fatalf("projectID = %q, want project-1", projectID)
			}
			return ProjectRecord{}, ErrNotFound
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodGet, "/projects/project-1", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusNotFound, "not_found")
}

func TestViewerUpdateProjectReturns403(t *testing.T) {
	projectService := &fakeProjectService{
		updateProject: func(_ context.Context, actorUserID, projectID string, input UpdateProjectInput) (ProjectRecord, error) {
			if actorUserID != "user-1" {
				t.Fatalf("actorUserID = %q, want user-1", actorUserID)
			}
			if projectID != "project-1" {
				t.Fatalf("projectID = %q, want project-1", projectID)
			}
			if input.Title == nil || *input.Title != "Changed title" {
				t.Fatalf("UpdateProjectInput.Title = %#v, want Changed title", input.Title)
			}
			return ProjectRecord{}, ErrForbidden
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPatch, "/projects/project-1", strings.NewReader(`{"title":"Changed title"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusForbidden, "forbidden")
}

func TestGetProjectHandlerReturnsOpenAPIProject(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC)
	projectService := &fakeProjectService{
		getProject: func(_ context.Context, actorUserID, projectID string) (ProjectRecord, error) {
			if actorUserID != "user-1" || projectID != "project-1" {
				t.Fatalf("GetProject(%q, %q), want user-1/project-1", actorUserID, projectID)
			}
			return ProjectRecord{
				ID:           "project-1",
				Title:        "The White Port",
				Description:  "City notes",
				BookCount:    4,
				ChapterCount: 12,
				WordCount:    34000,
				CreatedAt:    now,
				UpdatedAt:    now,
				CurrentMembership: ProjectMembershipRecord{
					ID:        "membership-1",
					ProjectID: "project-1",
					UserID:    "user-1",
					Role:      RoleViewer,
					Status:    MembershipStatusActive,
					CreatedAt: now,
					UpdatedAt: now,
				},
			}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodGet, "/projects/project-1", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var body struct {
		ID                string `json:"id"`
		Title             string `json:"title"`
		Description       string `json:"description"`
		BookCount         int    `json:"bookCount"`
		ChapterCount      int    `json:"chapterCount"`
		WordCount         int    `json:"wordCount"`
		CreatedAt         string `json:"createdAt"`
		UpdatedAt         string `json:"updatedAt"`
		CurrentMembership struct {
			ID        string `json:"id"`
			ProjectID string `json:"projectId"`
			UserID    string `json:"userId"`
			Role      Role   `json:"role"`
			Status    string `json:"status"`
			CreatedAt string `json:"createdAt"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"currentMembership"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode project response: %v", err)
	}
	if body.ID != "project-1" || body.Title != "The White Port" || body.Description != "City notes" {
		t.Fatalf("project response = %#v, want project identity fields", body)
	}
	if body.BookCount != 4 || body.ChapterCount != 12 || body.WordCount != 34000 {
		t.Fatalf("project counts = %d/%d/%d, want 4/12/34000", body.BookCount, body.ChapterCount, body.WordCount)
	}
	if body.CreatedAt != "2026-06-24T12:30:00.000Z" || body.UpdatedAt != "2026-06-24T12:30:00.000Z" {
		t.Fatalf("timestamps = %q/%q, want RFC3339 millis UTC", body.CreatedAt, body.UpdatedAt)
	}
	if body.CurrentMembership.ID != "membership-1" ||
		body.CurrentMembership.ProjectID != "project-1" ||
		body.CurrentMembership.UserID != "user-1" ||
		body.CurrentMembership.Role != RoleViewer ||
		body.CurrentMembership.Status != string(MembershipStatusActive) {
		t.Fatalf("currentMembership = %#v, want viewer membership", body.CurrentMembership)
	}
}

func TestListProjectsHandlerReturnsOpenAPIProjectList(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC)
	nextCursor := "cursor-out"
	projectService := &fakeProjectService{
		listProjects: func(_ context.Context, actorUserID string, input ListProjectsInput) (ProjectPage, error) {
			if actorUserID != "user-1" {
				t.Fatalf("ListProjects actorUserID = %q, want user-1", actorUserID)
			}
			if input.Limit != 1 || input.Cursor != "cursor-in" {
				t.Fatalf("ListProjects input = %#v, want requested limit and cursor", input)
			}
			return ProjectPage{
				Data: []ProjectRecord{{
					ID:           "project-1",
					Title:        "The White Port",
					Description:  "City notes",
					BookCount:    4,
					ChapterCount: 12,
					WordCount:    34000,
					CreatedAt:    now,
					UpdatedAt:    now,
					CurrentMembership: ProjectMembershipRecord{
						ID:        "membership-1",
						ProjectID: "project-1",
						UserID:    "user-1",
						Role:      RoleOwner,
						Status:    MembershipStatusActive,
						CreatedAt: now,
						UpdatedAt: now,
					},
				}},
				Meta: PageMeta{HasMore: true, NextCursor: &nextCursor},
			}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodGet, "/projects?limit=1&cursor=cursor-in", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var body struct {
		Data []projectResponse `json:"data"`
		Meta pageMetaResponse  `json:"meta"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode project list response: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "project-1" || body.Data[0].CurrentMembership.Role != RoleOwner {
		t.Fatalf("project list response = %#v, want one owned project", body.Data)
	}
	if !body.Meta.HasMore || body.Meta.NextCursor == nil || *body.Meta.NextCursor != nextCursor {
		t.Fatalf("meta = %#v, want cursor pagination", body.Meta)
	}
}

func TestListProjectsMissingSessionReturns401(t *testing.T) {
	projectService := &fakeProjectService{
		listProjects: func(context.Context, string, ListProjectsInput) (ProjectPage, error) {
			t.Fatal("ListProjects must not be called without an authenticated session")
			return ProjectPage{}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, &fakeProjectAuthService{
		currentUserErr: auth.ErrUnauthenticated,
	})
	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusUnauthorized, "auth.session_required")
}

func TestListProjectsDeletedStateReturnsRestoreClocks(t *testing.T) {
	deletedAt := time.Date(2026, 7, 12, 12, 30, 0, 0, time.UTC)
	restoreUntil := deletedAt.Add(7 * 24 * time.Hour)
	projectService := &fakeProjectService{
		listProjects: func(_ context.Context, actorUserID string, input ListProjectsInput) (ProjectPage, error) {
			if actorUserID != "user-1" || input.State != ProjectListDeleted {
				t.Fatalf("ListProjects actor/input = %q %#v", actorUserID, input)
			}
			return ProjectPage{Data: []ProjectRecord{{
				ID:           "project-1",
				Title:        "Deleted Project",
				CreatedAt:    deletedAt.Add(-time.Hour),
				UpdatedAt:    deletedAt,
				DeletedAt:    &deletedAt,
				RestoreUntil: &restoreUntil,
				CurrentMembership: ProjectMembershipRecord{
					ID: "membership-1", ProjectID: "project-1", UserID: "user-1",
					Role: RoleOwner, Status: MembershipStatusActive,
					CreatedAt: deletedAt.Add(-time.Hour), UpdatedAt: deletedAt.Add(-time.Hour),
				},
			}}}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodGet, "/projects?state=deleted", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	payload := rec.Body.Bytes()
	var exactBody struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &exactBody); err != nil {
		t.Fatal(err)
	}
	if len(exactBody.Data) != 1 {
		t.Fatalf("deleted response rows = %d, want 1", len(exactBody.Data))
	}
	wantKeys := []string{"id", "title", "deletedAt", "restoreUntil"}
	if len(exactBody.Data[0]) != len(wantKeys) {
		t.Fatalf("deleted response keys = %v, want %v", exactBody.Data[0], wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := exactBody.Data[0][key]; !ok {
			t.Fatalf("deleted response missing key %q: %v", key, exactBody.Data[0])
		}
	}

	var body struct {
		Data []struct {
			DeletedAt    *string `json:"deletedAt"`
			RestoreUntil *string `json:"restoreUntil"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].DeletedAt == nil || *body.Data[0].DeletedAt != "2026-07-12T12:30:00.000Z" ||
		body.Data[0].RestoreUntil == nil || *body.Data[0].RestoreUntil != "2026-07-19T12:30:00.000Z" {
		t.Fatalf("deleted response = %#v", body.Data)
	}
}

func TestListProjectsRejectsUnknownStateBeforeService(t *testing.T) {
	projectService := &fakeProjectService{
		listProjects: func(context.Context, string, ListProjectsInput) (ProjectPage, error) {
			t.Fatal("ListProjects must not be called for an unknown state")
			return ProjectPage{}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodGet, "/projects?state=archived", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusBadRequest, "request.validation")
}

func TestRestoreProjectHandlerRequiresCSRFAndReturnsActiveProject(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 30, 0, 0, time.UTC)
	called := 0
	projectService := &fakeProjectService{
		restoreProject: func(_ context.Context, actorUserID, projectID string) (ProjectRecord, error) {
			called++
			if actorUserID != "user-1" || projectID != "project-1" {
				t.Fatalf("RestoreProject actor/project = %q/%q", actorUserID, projectID)
			}
			return ProjectRecord{
				ID: "project-1", Title: "Restored", CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
				CurrentMembership: ProjectMembershipRecord{
					ID: "membership-1", ProjectID: "project-1", UserID: "user-1",
					Role: RoleOwner, Status: MembershipStatusActive,
					CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
				},
			}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))

	withoutCSRF := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/projects/project-1/restore", nil)
	withoutCSRF.Header.Set("Origin", projectAllowedOriginForTest)
	withoutCSRFRecorder := httptest.NewRecorder()
	handler.ServeHTTP(withoutCSRFRecorder, withoutCSRF)
	assertProblemForTest(t, withoutCSRFRecorder, http.StatusForbidden, "csrf.required")
	if called != 0 {
		t.Fatal("RestoreProject was called without CSRF")
	}

	request := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/projects/project-1/restore", nil)
	request.Header.Set("Origin", projectAllowedOriginForTest)
	request.Header.Set("X-CSRF-Token", opaqueValueForTest())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body.String())
	}
	if called != 1 {
		t.Fatalf("RestoreProject calls = %d, want 1", called)
	}
	var body projectResponse
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "project-1" || body.Title != "Restored" {
		t.Fatalf("restore response = %#v", body)
	}
}

func TestRestoreProjectHandlerMapsAuthorizationBeforeLifecycleOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		status     int
		code       string
	}{
		{name: "missing or non-member concealed", serviceErr: ErrNotFound, status: http.StatusNotFound, code: "not_found"},
		{name: "insufficient role forbidden", serviceErr: ErrForbidden, status: http.StatusForbidden, code: "forbidden"},
		{name: "lifecycle conflict after proof", serviceErr: ErrConflict, status: http.StatusConflict, code: "conflict"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			projectService := &fakeProjectService{restoreProject: func(context.Context, string, string) (ProjectRecord, error) {
				calls++
				return ProjectRecord{}, tt.serviceErr
			}}
			handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
			request := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/projects/project-1/restore", nil)
			request.Header.Set("Origin", projectAllowedOriginForTest)
			request.Header.Set("X-CSRF-Token", "csrf-for-test")
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			assertProblemForTest(t, recorder, tt.status, tt.code)
			if calls != 1 {
				t.Fatalf("RestoreProject calls = %d, want 1", calls)
			}
		})
	}
}

func TestListMyProjectInvitationsHandlerPassesPaginationAndReturnsMeta(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC)
	nextCursor := "cursor-out"
	projectService := &fakeProjectService{
		listMyProjectInvitations: func(_ context.Context, actor AuthenticatedUser, input ListMyProjectInvitationsInput) (ProjectInvitationPage, error) {
			if actor.ID != "user-1" || actor.Email != "writer@example.test" || !actor.EmailVerified {
				t.Fatalf("ListMyProjectInvitations actor = %#v, want current verified user", actor)
			}
			if input.Limit != 1 || input.Cursor != "cursor-in" {
				t.Fatalf("ListMyProjectInvitations input = %#v, want requested limit and cursor", input)
			}
			return ProjectInvitationPage{
				Data: []ProjectInvitationRecord{{
					ID:           "invitation-1",
					ProjectID:    "project-1",
					ProjectTitle: "The White Port",
					Email:        "writer@example.test",
					Role:         RoleViewer,
					Status:       ProjectInvitationStatusPending,
					ExpiresAt:    now.Add(time.Hour),
					CreatedAt:    now,
					UpdatedAt:    now,
				}},
				Meta: PageMeta{HasMore: true, NextCursor: &nextCursor},
			}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodGet, "/project-invitations?limit=1&cursor=cursor-in", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body projectInvitationListResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode invitation list response: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "invitation-1" {
		t.Fatalf("invitation list response = %#v, want invitation-1", body.Data)
	}
	if !body.Meta.HasMore || body.Meta.NextCursor == nil || *body.Meta.NextCursor != nextCursor {
		t.Fatalf("meta = %#v, want cursor pagination", body.Meta)
	}
}

func TestCreateProjectHandlerReturnsOpenAPIProject(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC)
	projectService := &fakeProjectService{
		createProject: func(_ context.Context, actorUserID string, input CreateProjectInput) (ProjectRecord, error) {
			if actorUserID != "user-1" {
				t.Fatalf("CreateProject actorUserID = %q, want user-1", actorUserID)
			}
			if input.Title != "The White Port" || input.Description != "City notes" {
				t.Fatalf("CreateProject input = %#v, want title and description", input)
			}
			return ProjectRecord{
				ID:          "project-1",
				Title:       "The White Port",
				Description: "City notes",
				CreatedAt:   now,
				UpdatedAt:   now,
				CurrentMembership: ProjectMembershipRecord{
					ID:        "membership-1",
					ProjectID: "project-1",
					UserID:    "user-1",
					Role:      RoleOwner,
					Status:    MembershipStatusActive,
					CreatedAt: now,
					UpdatedAt: now,
				},
			}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/projects", strings.NewReader(`{"title":"The White Port","description":"City notes"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var body projectResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode project response: %v", err)
	}
	if body.ID != "project-1" || body.Title != "The White Port" || body.CurrentMembership.Role != RoleOwner {
		t.Fatalf("created project response = %#v, want owner project", body)
	}
}

func TestCreateProjectSessionAndCSRFGates(t *testing.T) {
	tests := []struct {
		name        string
		authService *fakeProjectAuthService
		withSession bool
		withCSRF    bool
		wantStatus  int
		wantCode    string
	}{
		{
			name: "missing session before csrf",
			authService: &fakeProjectAuthService{
				currentUserErr: auth.ErrUnauthenticated,
				validateErr:    auth.ErrInvalidCSRF,
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "auth.session_required",
		},
		{
			name:        "missing csrf with valid session",
			authService: fakeProjectAuthForTest("user-1"),
			withSession: true,
			wantStatus:  http.StatusForbidden,
			wantCode:    "csrf.required",
		},
		{
			name: "invalid csrf with valid session",
			authService: func() *fakeProjectAuthService {
				authService := fakeProjectAuthForTest("user-1")
				authService.validateErr = auth.ErrInvalidCSRF
				return authService
			}(),
			withSession: true,
			withCSRF:    true,
			wantStatus:  http.StatusForbidden,
			wantCode:    "csrf.invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectService := &fakeProjectService{
				createProject: func(context.Context, string, CreateProjectInput) (ProjectRecord, error) {
					t.Fatal("CreateProject must not be called before session and CSRF gates pass")
					return ProjectRecord{}, nil
				},
			}
			handler := newProjectHandlerForTest(projectService, tt.authService)
			req := httptest.NewRequest(http.MethodPost, "/projects", strings.NewReader(`{"title":"The White Port"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", projectAllowedOriginForTest)
			if tt.withSession {
				req.AddCookie(&http.Cookie{
					Name:  auth.SessionCookieName,
					Value: opaqueValueForTest(),
				})
			}
			if tt.withCSRF {
				req.Header.Set("X-CSRF-Token", opaqueValueForTest())
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertProblemForTest(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestCreateProjectValidationReturns400(t *testing.T) {
	projectService := &fakeProjectService{
		createProject: func(context.Context, string, CreateProjectInput) (ProjectRecord, error) {
			return ProjectRecord{}, ErrValidation
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/projects", strings.NewReader(`{"title":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusBadRequest, "request.validation")
}

func TestUpdateProjectMapsNullDescription(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC)
	projectService := &fakeProjectService{
		updateProject: func(_ context.Context, actorUserID, projectID string, input UpdateProjectInput) (ProjectRecord, error) {
			if actorUserID != "user-1" || projectID != "project-1" {
				t.Fatalf("UpdateProject(%q, %q), want user-1/project-1", actorUserID, projectID)
			}
			if !input.DescriptionSet {
				t.Fatal("DescriptionSet = false, want true for explicit null description")
			}
			if input.Description != nil {
				t.Fatalf("Description = %#v, want nil for explicit null description", input.Description)
			}
			if input.Title != nil {
				t.Fatalf("Title = %#v, want nil for absent title", input.Title)
			}
			return ProjectRecord{
				ID:           "project-1",
				Title:        "The White Port",
				Description:  "",
				BookCount:    4,
				ChapterCount: 12,
				WordCount:    34000,
				CreatedAt:    now,
				UpdatedAt:    now,
				CurrentMembership: ProjectMembershipRecord{
					ID:        "membership-1",
					ProjectID: "project-1",
					UserID:    "user-1",
					Role:      RoleEditor,
					Status:    MembershipStatusActive,
					CreatedAt: now,
					UpdatedAt: now,
				},
			}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPatch, "/projects/project-1", strings.NewReader(`{"description":null}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestUpdateProjectUnknownFieldReturnsInvalidRequest(t *testing.T) {
	projectService := &fakeProjectService{
		updateProject: func(context.Context, string, string, UpdateProjectInput) (ProjectRecord, error) {
			t.Fatal("UpdateProject must not be called for malformed JSON")
			return ProjectRecord{}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPatch, "/projects/project-1", strings.NewReader(`{"title":"Changed","unexpected":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusBadRequest, "request.invalid")
}

func TestUpdateProjectValidationReturns400(t *testing.T) {
	projectService := &fakeProjectService{
		updateProject: func(context.Context, string, string, UpdateProjectInput) (ProjectRecord, error) {
			return ProjectRecord{}, ErrValidation
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPatch, "/projects/project-1", strings.NewReader(`{"title":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusBadRequest, "request.validation")
}

func TestCreateProjectInvitationSuccessReturnsOpenAPIInvitation(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC)
	projectService := &fakeProjectService{
		createProjectInvitation: func(_ context.Context, actorUserID, projectID string, input CreateProjectInvitationInput) (ProjectInvitationRecord, error) {
			if actorUserID != "user-1" || projectID != "project-1" {
				t.Fatalf("CreateProjectInvitation(%q, %q), want user-1/project-1", actorUserID, projectID)
			}
			if input.Email != "Invitee@Example.TEST" || input.Role != RoleEditor {
				t.Fatalf("CreateProjectInvitation input role = %s, want editor", input.Role)
			}
			return ProjectInvitationRecord{
				ID:                 "invitation-1",
				ProjectID:          "project-1",
				ProjectTitle:       "The White Port",
				InviterDisplayName: "Writer",
				InviterEmail:       "writer@example.test",
				Email:              "invitee@example.test",
				Role:               RoleEditor,
				Status:             ProjectInvitationStatusPending,
				ExpiresAt:          now.Add(14 * 24 * time.Hour),
				CreatedAt:          now,
				UpdatedAt:          now,
			}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/projects/project-1/invitations", strings.NewReader(`{"email":"Invitee@Example.TEST","role":"editor"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var body struct {
		ID        string `json:"id"`
		ProjectID string `json:"projectId"`
		Project   struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"project"`
		Inviter struct {
			DisplayName string `json:"displayName"`
			Email       string `json:"email"`
		} `json:"inviter"`
		Email     string `json:"email"`
		Role      Role   `json:"role"`
		Status    string `json:"status"`
		ExpiresAt string `json:"expiresAt"`
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode invitation response: %v", err)
	}
	if body.ID != "invitation-1" || body.ProjectID != "project-1" || body.Project.ID != "project-1" || body.Project.Title != "The White Port" {
		t.Fatalf("invitation project response = %q/%q/%q/%q, want invitation/project context", body.ID, body.ProjectID, body.Project.ID, body.Project.Title)
	}
	if body.Inviter.DisplayName != "Writer" || body.Inviter.Email != "writer@example.test" {
		t.Fatalf("inviter displayName = %q, want Writer", body.Inviter.DisplayName)
	}
	if body.Email != "invitee@example.test" || body.Role != RoleEditor || body.Status != string(ProjectInvitationStatusPending) {
		t.Fatalf("invitation role/status = %s/%s, want editor/pending", body.Role, body.Status)
	}
	if body.ExpiresAt != "2026-07-08T12:30:00.000Z" || body.CreatedAt != "2026-06-24T12:30:00.000Z" || body.UpdatedAt != "2026-06-24T12:30:00.000Z" {
		t.Fatalf("invitation timestamps = %q/%q/%q, want RFC3339 millis UTC", body.ExpiresAt, body.CreatedAt, body.UpdatedAt)
	}
}

func TestCreateProjectInvitationEditorReturns403(t *testing.T) {
	projectService := &fakeProjectService{
		createProjectInvitation: func(context.Context, string, string, CreateProjectInvitationInput) (ProjectInvitationRecord, error) {
			return ProjectInvitationRecord{}, ErrForbidden
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/projects/project-1/invitations", strings.NewReader(`{"email":"invitee@example.test","role":"viewer"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusForbidden, "forbidden")
}

func TestCreateProjectInvitationNonMemberReturns404(t *testing.T) {
	projectService := &fakeProjectService{
		createProjectInvitation: func(context.Context, string, string, CreateProjectInvitationInput) (ProjectInvitationRecord, error) {
			return ProjectInvitationRecord{}, ErrNotFound
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/projects/project-1/invitations", strings.NewReader(`{"email":"invitee@example.test","role":"viewer"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusNotFound, "not_found")
}

func TestAcceptProjectInvitationSuccessReturnsMembership(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC)
	projectService := &fakeProjectService{
		acceptProjectInvitation: func(_ context.Context, actor AuthenticatedUser, invitationID string) (ProjectMembershipRecord, error) {
			if actor.ID != "user-1" || actor.Email != "writer@example.test" || !actor.EmailVerified {
				t.Fatalf("actor id/verified = %q/%t, want user-1/true", actor.ID, actor.EmailVerified)
			}
			if invitationID != "invitation-1" {
				t.Fatalf("invitationID = %q, want invitation-1", invitationID)
			}
			return ProjectMembershipRecord{
				ID:        "membership-1",
				ProjectID: "project-1",
				UserID:    "user-1",
				Role:      RoleViewer,
				Status:    MembershipStatusActive,
				CreatedAt: now,
				UpdatedAt: now,
			}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/project-invitations/invitation-1/accept", strings.NewReader(""))
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body projectMembershipResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode membership response: %v", err)
	}
	if body.ID != "membership-1" || body.ProjectID != "project-1" || body.UserID != "user-1" || body.Role != RoleViewer || body.Status != MembershipStatusActive {
		t.Fatalf("membership response = %#v, want accepted viewer membership", body)
	}
}

func TestListProjectMembersHandlerReturnsPIIForManagers(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC)
	displayName := "Reader"
	projectService := &fakeProjectService{
		listProjectMembers: func(_ context.Context, actorUserID, projectID string, input ListProjectMembersInput) ([]ProjectMembershipRecord, error) {
			if actorUserID != "user-1" || projectID != "project-1" {
				t.Fatalf("ListProjectMembers(%q, %q), want user-1/project-1", actorUserID, projectID)
			}
			if input.Limit != 50 || input.Cursor != "" {
				t.Fatalf("ListProjectMembers input = %#v, want default limit and no cursor", input)
			}
			return []ProjectMembershipRecord{
				{
					ID:          "membership-2",
					ProjectID:   "project-1",
					UserID:      "user-2",
					Email:       "reader@example.test",
					DisplayName: &displayName,
					Role:        RoleViewer,
					Status:      MembershipStatusActive,
					CreatedAt:   now,
					UpdatedAt:   now,
				},
			}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodGet, "/projects/project-1/members", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Data []projectMembershipResponse `json:"data"`
		Meta pageMetaResponse            `json:"meta"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode member list response: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].Email != "reader@example.test" || body.Data[0].DisplayName == nil || *body.Data[0].DisplayName != "Reader" {
		t.Fatalf("member list response = %#v, want member PII for manager", body.Data)
	}
	if body.Meta.HasMore || body.Meta.NextCursor != nil {
		t.Fatalf("meta = %#v, want no cursor pagination", body.Meta)
	}
}

func TestUpdateProjectMemberRoleHandlerReturnsMembership(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC)
	projectService := &fakeProjectService{
		updateProjectMemberRole: func(_ context.Context, actorUserID, projectID, memberID string, input UpdateProjectMemberRoleInput) (ProjectMembershipRecord, error) {
			if actorUserID != "user-1" || projectID != "project-1" || memberID != "membership-2" {
				t.Fatalf("UpdateProjectMemberRole(%q, %q, %q), want user-1/project-1/membership-2", actorUserID, projectID, memberID)
			}
			if input.Role != RoleEditor {
				t.Fatalf("UpdateProjectMemberRole role = %s, want editor", input.Role)
			}
			return ProjectMembershipRecord{
				ID:        "membership-2",
				ProjectID: "project-1",
				UserID:    "user-2",
				Role:      RoleEditor,
				Status:    MembershipStatusActive,
				CreatedAt: now,
				UpdatedAt: now,
			}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPatch, "/projects/project-1/members/membership-2", strings.NewReader(`{"role":"editor"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body projectMembershipResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode member response: %v", err)
	}
	if body.ID != "membership-2" || body.Role != RoleEditor || body.Status != MembershipStatusActive {
		t.Fatalf("updated member response = %#v, want active editor", body)
	}
}

func TestRemoveProjectMemberHandlerReturnsNoContent(t *testing.T) {
	projectService := &fakeProjectService{
		removeProjectMember: func(_ context.Context, actorUserID, projectID, memberID string) error {
			if actorUserID != "user-1" || projectID != "project-1" || memberID != "membership-2" {
				t.Fatalf("RemoveProjectMember(%q, %q, %q), want user-1/project-1/membership-2", actorUserID, projectID, memberID)
			}
			return nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodDelete, "/projects/project-1/members/membership-2", nil)
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body length = %d, want 0", rec.Body.Len())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
}

func TestProjectMemberHandlersMapAuthorizationErrors(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		body       string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{name: "list viewer forbidden", method: http.MethodGet, target: "/projects/project-1/members", serviceErr: ErrForbidden, wantStatus: http.StatusForbidden, wantCode: "forbidden"},
		{name: "update owner conflict", method: http.MethodPatch, target: "/projects/project-1/members/membership-owner", body: `{"role":"viewer"}`, serviceErr: ErrConflict, wantStatus: http.StatusConflict, wantCode: "conflict"},
		{name: "remove cross project not found", method: http.MethodDelete, target: "/projects/project-1/members/membership-other", serviceErr: ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "not_found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectService := &fakeProjectService{
				listProjectMembers: func(context.Context, string, string, ListProjectMembersInput) ([]ProjectMembershipRecord, error) {
					return nil, tt.serviceErr
				},
				updateProjectMemberRole: func(context.Context, string, string, string, UpdateProjectMemberRoleInput) (ProjectMembershipRecord, error) {
					return ProjectMembershipRecord{}, tt.serviceErr
				},
				removeProjectMember: func(context.Context, string, string, string) error {
					return tt.serviceErr
				},
			}
			handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
			req := newAuthenticatedProjectRequestForTest(t, tt.method, tt.target, strings.NewReader(tt.body))
			if tt.method != http.MethodGet {
				req.Header.Set("Origin", projectAllowedOriginForTest)
				req.Header.Set("X-CSRF-Token", opaqueValueForTest())
				if tt.body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertProblemForTest(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestDeleteProjectSuccessReturnsNoContentNoStore(t *testing.T) {
	projectService := &fakeProjectService{
		deleteProject: func(_ context.Context, actorUserID, projectID string) error {
			if actorUserID != "user-1" || projectID != "project-1" {
				t.Fatalf("DeleteProject(%q, %q), want user-1/project-1", actorUserID, projectID)
			}
			return nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	req := newAuthenticatedProjectRequestForTest(t, http.MethodDelete, "/projects/project-1", nil)
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body length = %d, want 0", rec.Body.Len())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
}

func TestUnsafeInvitationMutationInvalidOrMissingSessionBeforeMissingCSRFReturns401(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		body        string
		withSession bool
	}{
		{
			name:   "create missing session",
			target: "/projects/project-1/invitations",
			body:   `{"email":"invitee@example.test","role":"viewer"}`,
		},
		{
			name:        "create invalid session",
			target:      "/projects/project-1/invitations",
			body:        `{"email":"invitee@example.test","role":"viewer"}`,
			withSession: true,
		},
		{
			name:   "cancel missing session",
			target: "/project-invitations/invitation-1/cancel",
		},
		{
			name:        "cancel invalid session",
			target:      "/project-invitations/invitation-1/cancel",
			withSession: true,
		},
		{
			name:   "accept missing session",
			target: "/project-invitations/invitation-1/accept",
		},
		{
			name:        "accept invalid session",
			target:      "/project-invitations/invitation-1/accept",
			withSession: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectService := &fakeProjectService{
				createProjectInvitation: func(context.Context, string, string, CreateProjectInvitationInput) (ProjectInvitationRecord, error) {
					t.Fatal("CreateProjectInvitation must not be called without an authenticated session")
					return ProjectInvitationRecord{}, nil
				},
				cancelProjectInvitation: func(context.Context, string, string) (ProjectInvitationRecord, error) {
					t.Fatal("CancelProjectInvitation must not be called without an authenticated session")
					return ProjectInvitationRecord{}, nil
				},
				acceptProjectInvitation: func(context.Context, AuthenticatedUser, string) (ProjectMembershipRecord, error) {
					t.Fatal("AcceptProjectInvitation must not be called without an authenticated session")
					return ProjectMembershipRecord{}, nil
				},
			}
			handler := newProjectHandlerForTest(projectService, &fakeProjectAuthService{
				currentUserErr: auth.ErrUnauthenticated,
				validateErr:    auth.ErrInvalidCSRF,
			})
			req := httptest.NewRequest(http.MethodPost, tt.target, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", projectAllowedOriginForTest)
			if tt.withSession {
				req.AddCookie(&http.Cookie{
					Name:  auth.SessionCookieName,
					Value: opaqueValueForTest(),
				})
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertProblemForTest(t, rec, http.StatusUnauthorized, "auth.session_required")
		})
	}
}

func TestUnsafeProjectMutationInvalidOrMissingSessionBeforeMissingCSRFReturns401(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		withSession bool
	}{
		{
			name:   "patch missing session",
			method: http.MethodPatch,
		},
		{
			name:        "patch invalid session",
			method:      http.MethodPatch,
			withSession: true,
		},
		{
			name:   "delete missing session",
			method: http.MethodDelete,
		},
		{
			name:        "delete invalid session",
			method:      http.MethodDelete,
			withSession: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectService := &fakeProjectService{
				updateProject: func(context.Context, string, string, UpdateProjectInput) (ProjectRecord, error) {
					t.Fatal("UpdateProject must not be called without an authenticated session")
					return ProjectRecord{}, nil
				},
				deleteProject: func(context.Context, string, string) error {
					t.Fatal("DeleteProject must not be called without an authenticated session")
					return nil
				},
			}
			handler := newProjectHandlerForTest(projectService, &fakeProjectAuthService{
				currentUserErr: auth.ErrUnauthenticated,
				validateErr:    auth.ErrInvalidCSRF,
			})
			req := newProjectMutationRequestWithCSRFForTest(t, tt.method, tt.withSession, false)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertProblemForTest(t, rec, http.StatusUnauthorized, "auth.session_required")
		})
	}
}

func TestUnsafeProjectMutationInvalidOrMissingSessionReturns401(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		withSession bool
	}{
		{
			name:   "patch missing session",
			method: http.MethodPatch,
		},
		{
			name:        "patch invalid session",
			method:      http.MethodPatch,
			withSession: true,
		},
		{
			name:   "delete missing session",
			method: http.MethodDelete,
		},
		{
			name:        "delete invalid session",
			method:      http.MethodDelete,
			withSession: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectService := &fakeProjectService{
				updateProject: func(context.Context, string, string, UpdateProjectInput) (ProjectRecord, error) {
					t.Fatal("UpdateProject must not be called without an authenticated session")
					return ProjectRecord{}, nil
				},
				deleteProject: func(context.Context, string, string) error {
					t.Fatal("DeleteProject must not be called without an authenticated session")
					return nil
				},
			}
			handler := newProjectHandlerForTest(projectService, &fakeProjectAuthService{
				currentUserErr: auth.ErrUnauthenticated,
				validateErr:    auth.ErrInvalidCSRF,
			})
			req := newProjectMutationRequestForTest(t, tt.method, tt.withSession)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertProblemForTest(t, rec, http.StatusUnauthorized, "auth.session_required")
		})
	}
}

func TestUnsafeProjectMutationMissingCSRFWithValidSessionReturns403(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{
			name:   "patch",
			method: http.MethodPatch,
		},
		{
			name:   "delete",
			method: http.MethodDelete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectService := &fakeProjectService{
				updateProject: func(context.Context, string, string, UpdateProjectInput) (ProjectRecord, error) {
					t.Fatal("UpdateProject must not be called with missing CSRF")
					return ProjectRecord{}, nil
				},
				deleteProject: func(context.Context, string, string) error {
					t.Fatal("DeleteProject must not be called with missing CSRF")
					return nil
				},
			}
			handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
			req := newProjectMutationRequestWithCSRFForTest(t, tt.method, true, false)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertProblemForTest(t, rec, http.StatusForbidden, "csrf.required")
		})
	}
}

func TestUnsafeProjectMutationInvalidCSRFWithValidSessionReturns403(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{
			name:   "patch",
			method: http.MethodPatch,
		},
		{
			name:   "delete",
			method: http.MethodDelete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectService := &fakeProjectService{
				updateProject: func(context.Context, string, string, UpdateProjectInput) (ProjectRecord, error) {
					t.Fatal("UpdateProject must not be called with invalid CSRF")
					return ProjectRecord{}, nil
				},
				deleteProject: func(context.Context, string, string) error {
					t.Fatal("DeleteProject must not be called with invalid CSRF")
					return nil
				},
			}
			authService := fakeProjectAuthForTest("user-1")
			authService.validateErr = auth.ErrInvalidCSRF
			handler := newProjectHandlerForTest(projectService, authService)
			req := newProjectMutationRequestForTest(t, tt.method, true)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertProblemForTest(t, rec, http.StatusForbidden, "csrf.invalid")
		})
	}
}

func TestGetProjectMissingSessionReturns401(t *testing.T) {
	handler := newProjectHandlerForTest(&fakeProjectService{}, &fakeProjectAuthService{
		currentUserErr: auth.ErrUnauthenticated,
	})
	req := httptest.NewRequest(http.MethodGet, "/projects/project-1", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusUnauthorized, "auth.session_required")
}

type fakeProjectService struct {
	listProjects             func(context.Context, string, ListProjectsInput) (ProjectPage, error)
	createProject            func(context.Context, string, CreateProjectInput) (ProjectRecord, error)
	getProject               func(context.Context, string, string) (ProjectRecord, error)
	updateProject            func(context.Context, string, string, UpdateProjectInput) (ProjectRecord, error)
	deleteProject            func(context.Context, string, string) error
	restoreProject           func(context.Context, string, string) (ProjectRecord, error)
	listProjectMembers       func(context.Context, string, string, ListProjectMembersInput) ([]ProjectMembershipRecord, error)
	updateProjectMemberRole  func(context.Context, string, string, string, UpdateProjectMemberRoleInput) (ProjectMembershipRecord, error)
	removeProjectMember      func(context.Context, string, string, string) error
	listProjectInvitations   func(context.Context, string, string) ([]ProjectInvitationRecord, error)
	createProjectInvitation  func(context.Context, string, string, CreateProjectInvitationInput) (ProjectInvitationRecord, error)
	cancelProjectInvitation  func(context.Context, string, string) (ProjectInvitationRecord, error)
	acceptProjectInvitation  func(context.Context, AuthenticatedUser, string) (ProjectMembershipRecord, error)
	listMyProjectInvitations func(context.Context, AuthenticatedUser, ListMyProjectInvitationsInput) (ProjectInvitationPage, error)
}

func (s *fakeProjectService) ListProjects(ctx context.Context, actorUserID string, input ListProjectsInput) (ProjectPage, error) {
	if s.listProjects == nil {
		return ProjectPage{}, errors.New("unexpected ListProjects call")
	}
	return s.listProjects(ctx, actorUserID, input)
}

func (s *fakeProjectService) CreateProject(ctx context.Context, actorUserID string, input CreateProjectInput) (ProjectRecord, error) {
	if s.createProject == nil {
		return ProjectRecord{}, errors.New("unexpected CreateProject call")
	}
	return s.createProject(ctx, actorUserID, input)
}

func (s *fakeProjectService) GetProject(ctx context.Context, actorUserID, projectID string) (ProjectRecord, error) {
	if s.getProject == nil {
		return ProjectRecord{}, errors.New("unexpected GetProject call")
	}
	return s.getProject(ctx, actorUserID, projectID)
}

func (s *fakeProjectService) UpdateProject(ctx context.Context, actorUserID, projectID string, input UpdateProjectInput) (ProjectRecord, error) {
	if s.updateProject == nil {
		return ProjectRecord{}, errors.New("unexpected UpdateProject call")
	}
	return s.updateProject(ctx, actorUserID, projectID, input)
}

func (s *fakeProjectService) DeleteProject(ctx context.Context, actorUserID, projectID string) error {
	if s.deleteProject == nil {
		return errors.New("unexpected DeleteProject call")
	}
	return s.deleteProject(ctx, actorUserID, projectID)
}

func (s *fakeProjectService) RestoreProject(ctx context.Context, actorUserID, projectID string) (ProjectRecord, error) {
	if s.restoreProject == nil {
		return ProjectRecord{}, errors.New("unexpected RestoreProject call")
	}
	return s.restoreProject(ctx, actorUserID, projectID)
}

func (s *fakeProjectService) ListProjectMembers(ctx context.Context, actorUserID, projectID string, input ListProjectMembersInput) ([]ProjectMembershipRecord, error) {
	if s.listProjectMembers == nil {
		return nil, errors.New("unexpected ListProjectMembers call")
	}
	return s.listProjectMembers(ctx, actorUserID, projectID, input)
}

func (s *fakeProjectService) UpdateProjectMemberRole(ctx context.Context, actorUserID, projectID, memberID string, input UpdateProjectMemberRoleInput) (ProjectMembershipRecord, error) {
	if s.updateProjectMemberRole == nil {
		return ProjectMembershipRecord{}, errors.New("unexpected UpdateProjectMemberRole call")
	}
	return s.updateProjectMemberRole(ctx, actorUserID, projectID, memberID, input)
}

func (s *fakeProjectService) RemoveProjectMember(ctx context.Context, actorUserID, projectID, memberID string) error {
	if s.removeProjectMember == nil {
		return errors.New("unexpected RemoveProjectMember call")
	}
	return s.removeProjectMember(ctx, actorUserID, projectID, memberID)
}

func (s *fakeProjectService) ListProjectInvitations(ctx context.Context, actorUserID, projectID string) ([]ProjectInvitationRecord, error) {
	if s.listProjectInvitations == nil {
		return nil, errors.New("unexpected ListProjectInvitations call")
	}
	return s.listProjectInvitations(ctx, actorUserID, projectID)
}

func (s *fakeProjectService) CreateProjectInvitation(ctx context.Context, actorUserID, projectID string, input CreateProjectInvitationInput) (ProjectInvitationRecord, error) {
	if s.createProjectInvitation == nil {
		return ProjectInvitationRecord{}, errors.New("unexpected CreateProjectInvitation call")
	}
	return s.createProjectInvitation(ctx, actorUserID, projectID, input)
}

func (s *fakeProjectService) CancelProjectInvitation(ctx context.Context, actorUserID, invitationID string) (ProjectInvitationRecord, error) {
	if s.cancelProjectInvitation == nil {
		return ProjectInvitationRecord{}, errors.New("unexpected CancelProjectInvitation call")
	}
	return s.cancelProjectInvitation(ctx, actorUserID, invitationID)
}

func (s *fakeProjectService) AcceptProjectInvitation(ctx context.Context, actor AuthenticatedUser, invitationID string) (ProjectMembershipRecord, error) {
	if s.acceptProjectInvitation == nil {
		return ProjectMembershipRecord{}, errors.New("unexpected AcceptProjectInvitation call")
	}
	return s.acceptProjectInvitation(ctx, actor, invitationID)
}

func (s *fakeProjectService) ListMyProjectInvitations(ctx context.Context, actor AuthenticatedUser, input ListMyProjectInvitationsInput) (ProjectInvitationPage, error) {
	if s.listMyProjectInvitations == nil {
		return ProjectInvitationPage{}, errors.New("unexpected ListMyProjectInvitations call")
	}
	return s.listMyProjectInvitations(ctx, actor, input)
}

type fakeProjectAuthService struct {
	user           auth.User
	currentUserErr error
	validateErr    error
}

func fakeProjectAuthForTest(userID string) *fakeProjectAuthService {
	return &fakeProjectAuthService{
		user: auth.User{
			ID:            userID,
			Email:         "writer@example.test",
			EmailVerified: true,
			DisplayName:   "Writer",
			CreatedAt:     time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC),
		},
	}
}

func (s *fakeProjectAuthService) CurrentUser(context.Context, string) (auth.User, error) {
	if s.currentUserErr != nil {
		return auth.User{}, s.currentUserErr
	}
	return s.user, nil
}

func (s *fakeProjectAuthService) ValidateCSRF(context.Context, string, string) error {
	return s.validateErr
}

const projectAllowedOriginForTest = "https://app.example.test"

func newProjectHandlerForTest(projectService *fakeProjectService, authService *fakeProjectAuthService) http.Handler {
	router := chi.NewRouter()
	NewHandlers(projectService, authService, []string{projectAllowedOriginForTest}).Mount(router)
	return router
}

func newAuthenticatedProjectRequestForTest(t *testing.T, method, target string, body *strings.Reader) *http.Request {
	t.Helper()
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		reader = body
	}
	req := httptest.NewRequest(method, target, reader)
	req.AddCookie(&http.Cookie{
		Name:  auth.SessionCookieName,
		Value: opaqueValueForTest(),
	})
	return req
}

func newProjectMutationRequestForTest(t *testing.T, method string, withSession bool) *http.Request {
	t.Helper()
	return newProjectMutationRequestWithCSRFForTest(t, method, withSession, true)
}

func newProjectMutationRequestWithCSRFForTest(t *testing.T, method string, withSession bool, withCSRF bool) *http.Request {
	t.Helper()
	body := strings.NewReader("")
	if method == http.MethodPatch {
		body = strings.NewReader(`{"title":"Changed title"}`)
	}
	req := httptest.NewRequest(method, "/projects/project-1", body)
	if method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", projectAllowedOriginForTest)
	if withCSRF {
		req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	}
	if withSession {
		req.AddCookie(&http.Cookie{
			Name:  auth.SessionCookieName,
			Value: opaqueValueForTest(),
		})
	}
	return req
}

func opaqueValueForTest() string {
	return auth.NewSecureIDGenerator().NewCSRFToken()
}

func assertProblemForTest(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var problem struct {
		Status int    `json:"status"`
		Code   string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Status != status || problem.Code != code {
		t.Fatalf("problem = %#v, want status %d code %s", problem, status, code)
	}
}

func TestFakeProjectServicesImplementHandlerContracts(t *testing.T) {
	var _ ProjectService = (*fakeProjectService)(nil)
	var _ CurrentUserService = (*fakeProjectAuthService)(nil)
}

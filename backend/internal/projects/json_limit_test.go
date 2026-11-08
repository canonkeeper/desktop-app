package projects

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// SEC-15 (specs/2026-10-28-security-hardening.md, R2.1): a project handler reads at most 64 KiB of
// its JSON body. The padding is legal JSON whitespace, so only the size decides.
func TestCreateProjectReadsAtMostTheJSONBodyLimit(t *testing.T) {
	projectService := &fakeProjectService{
		createProject: func(context.Context, string, CreateProjectInput) (ProjectRecord, error) {
			t.Fatal("CreateProject must not be called for a body over the limit")
			return ProjectRecord{}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	body := `{"title":"The White Port",` + strings.Repeat(" ", 64<<10) + `"description":"City notes"}`
	req := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/projects", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", projectAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", opaqueValueForTest())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertProblemForTest(t, rec, http.StatusBadRequest, "request.invalid")
}

package projectaccess

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
)

func TestSupportPolicyHTTPDecisionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		cookie        string
		userErr       error
		membership    projects.Role
		membershipErr error
		wantStatus    int
	}{
		{name: "no session", userErr: auth.ErrUnauthenticated, wantStatus: http.StatusUnauthorized},
		{name: "active non member", cookie: "session", membershipErr: projects.ErrNotFound, wantStatus: http.StatusNotFound},
		{name: "viewer", cookie: "session", membership: projects.RoleViewer, wantStatus: http.StatusForbidden},
		{name: "editor", cookie: "session", membership: projects.RoleEditor, wantStatus: http.StatusForbidden},
		{name: "admin", cookie: "session", membership: projects.RoleAdmin, wantStatus: http.StatusNoContent},
		{name: "owner", cookie: "session", membership: projects.RoleOwner, wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := supportPolicyHandlerForTest{authorizer: NewAuthorizer(
				&fakeCurrentUserReader{user: auth.User{ID: "actor"}, err: tt.userErr},
				&fakeMembershipReader{role: tt.membership, err: tt.membershipErr},
			)}
			request := requestWithSessionCookie(tt.cookie)
			request.SetPathValue("projectId", "project-a")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("support handler status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
		})
	}
}

func TestSupportPolicyUsesCanInspectSupport(t *testing.T) {
	t.Parallel()

	for _, role := range []projects.Role{projects.RoleViewer, projects.RoleEditor, projects.RoleAdmin, projects.RoleOwner} {
		want := projects.CanInspectSupport(role)
		authorizer := NewAuthorizer(
			&fakeCurrentUserReader{user: auth.User{ID: "actor"}},
			&fakeMembershipReader{role: role},
		)
		_, decision, err := authorizer.AuthorizeRequest(
			t.Context(), requestWithSessionCookie("session"), "project-a",
			governance.SurfaceProjectSupportSnapshot, governance.ActionRead,
		)
		if err != nil {
			t.Fatalf("role %q: AuthorizeRequest() error = %v", role, err)
		}
		if got := decision == DecisionAllowed; got != want {
			t.Fatalf("role %q: allowed = %v, CanInspectSupport = %v", role, got, want)
		}
	}
}

type supportPolicyHandlerForTest struct{ authorizer Authorizer }

func (h supportPolicyHandlerForTest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, decision, err := h.authorizer.AuthorizeRequest(
		r.Context(), r, r.PathValue("projectId"),
		governance.SurfaceProjectSupportSnapshot, governance.ActionRead,
	)
	if err != nil {
		if !errors.Is(err, ErrAuthorizationUnavailable) && !errors.Is(err, ErrInvalidPolicy) {
			panic(err)
		}
		WriteProblem(w, Decision("internal"), "request-test")
		return
	}
	if decision != DecisionAllowed {
		WriteProblem(w, decision, "request-test")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

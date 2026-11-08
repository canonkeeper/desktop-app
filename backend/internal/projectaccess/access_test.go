package projectaccess

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
)

func TestAuthorizeRequestDecisionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		cookie        string
		user          auth.User
		userErr       error
		membership    projects.Role
		membershipErr error
		surface       governance.Surface
		action        governance.Action
		want          Decision
		wantErr       error
		wantUserCalls int
		wantRoleCalls int
	}{
		{name: "missing cookie", userErr: auth.ErrUnauthenticated, surface: governance.SurfaceProjectSupportSnapshot, action: governance.ActionRead, want: DecisionUnauthenticated, wantUserCalls: 1},
		{name: "unknown session", cookie: "unknown", userErr: auth.ErrUnauthenticated, surface: governance.SurfaceProjectSupportSnapshot, action: governance.ActionRead, want: DecisionUnauthenticated, wantUserCalls: 1},
		{name: "expired session", cookie: "expired", userErr: auth.ErrUnauthenticated, surface: governance.SurfaceProjectSupportSnapshot, action: governance.ActionRead, want: DecisionUnauthenticated, wantUserCalls: 1},
		{name: "active non member", cookie: "session", user: auth.User{ID: "user-a"}, membershipErr: projects.ErrNotFound, surface: governance.SurfaceProjectSupportSnapshot, action: governance.ActionRead, want: DecisionNotFound, wantUserCalls: 1, wantRoleCalls: 1},
		{name: "deleted project", cookie: "session", user: auth.User{ID: "user-a"}, membershipErr: projects.ErrNotFound, surface: governance.SurfaceProjectSupportSnapshot, action: governance.ActionRead, want: DecisionNotFound, wantUserCalls: 1, wantRoleCalls: 1},
		{name: "removed member", cookie: "session", user: auth.User{ID: "user-a"}, membershipErr: projects.ErrNotFound, surface: governance.SurfaceProjectSupportSnapshot, action: governance.ActionRead, want: DecisionNotFound, wantUserCalls: 1, wantRoleCalls: 1},
		{name: "viewer policy allows viewer", cookie: "session", user: auth.User{ID: "user-a"}, membership: projects.RoleViewer, surface: governance.SurfaceChatRead, action: governance.ActionRead, want: DecisionAllowed, wantUserCalls: 1, wantRoleCalls: 1},
		{name: "editor policy rejects viewer", cookie: "session", user: auth.User{ID: "user-a"}, membership: projects.RoleViewer, surface: governance.SurfaceChatMutation, action: governance.ActionMutate, want: DecisionForbidden, wantUserCalls: 1, wantRoleCalls: 1},
		{name: "editor policy allows editor", cookie: "session", user: auth.User{ID: "user-a"}, membership: projects.RoleEditor, surface: governance.SurfaceChatMutation, action: governance.ActionMutate, want: DecisionAllowed, wantUserCalls: 1, wantRoleCalls: 1},
		{name: "admin policy rejects editor", cookie: "session", user: auth.User{ID: "user-a"}, membership: projects.RoleEditor, surface: governance.SurfaceProjectSupportSnapshot, action: governance.ActionRead, want: DecisionForbidden, wantUserCalls: 1, wantRoleCalls: 1},
		{name: "admin policy allows admin", cookie: "session", user: auth.User{ID: "user-a"}, membership: projects.RoleAdmin, surface: governance.SurfaceProjectSupportSnapshot, action: governance.ActionRead, want: DecisionAllowed, wantUserCalls: 1, wantRoleCalls: 1},
		{name: "admin or owner policy rejects editor", cookie: "session", user: auth.User{ID: "user-a"}, membership: projects.RoleEditor, surface: governance.SurfaceProjectDelete, action: governance.ActionMutate, want: DecisionForbidden, wantUserCalls: 1, wantRoleCalls: 1},
		{name: "admin or owner policy allows owner", cookie: "session", user: auth.User{ID: "user-a"}, membership: projects.RoleOwner, surface: governance.SurfaceProjectDelete, action: governance.ActionMutate, want: DecisionAllowed, wantUserCalls: 1, wantRoleCalls: 1},
		{name: "binding policy fails closed", cookie: "session", surface: governance.SurfaceSourceChunkSDK, action: governance.ActionRead, wantErr: ErrInvalidPolicy},
		{name: "worker policy fails closed", cookie: "session", surface: governance.SurfaceRuntimeWorkflowStart, action: governance.ActionMutate, wantErr: ErrInvalidPolicy},
		{name: "no public policy fails closed", cookie: "session", surface: governance.SurfaceRetrievedContextSnapshot, action: governance.ActionRead, wantErr: ErrInvalidPolicy},
		{name: "exception surface fails closed", cookie: "session", surface: governance.SurfaceCancelOwnRunningChatTurn, action: governance.ActionMutate, wantErr: ErrInvalidPolicy},
		{name: "unknown surface fails closed", cookie: "session", surface: governance.Surface("unknown"), action: governance.ActionRead, wantErr: ErrInvalidPolicy},
		{name: "unknown action fails closed", cookie: "session", surface: governance.SurfaceChatRead, action: governance.Action("unknown"), wantErr: ErrInvalidPolicy},
		{name: "none policy fails closed", cookie: "session", surface: governance.SurfaceChatRead, action: governance.ActionMutate, wantErr: ErrInvalidPolicy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := &fakeCurrentUserReader{user: tt.user, err: tt.userErr}
			memberships := &fakeMembershipReader{role: tt.membership, err: tt.membershipErr}
			authorizer := NewAuthorizer(users, memberships)
			request := requestWithSessionCookie(tt.cookie)

			grant, decision, err := authorizer.AuthorizeRequest(t.Context(), request, "project-a", tt.surface, tt.action)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AuthorizeRequest() error = %v, want %v", err, tt.wantErr)
			}
			if decision != tt.want {
				t.Fatalf("AuthorizeRequest() decision = %q, want %q", decision, tt.want)
			}
			if users.calls != tt.wantUserCalls {
				t.Fatalf("CurrentUser() calls = %d, want %d", users.calls, tt.wantUserCalls)
			}
			if memberships.calls != tt.wantRoleCalls {
				t.Fatalf("ActiveProjectRole() calls = %d, want %d", memberships.calls, tt.wantRoleCalls)
			}
			if tt.want == DecisionAllowed {
				if grant.User.ID != tt.user.ID || grant.Role != tt.membership {
					t.Fatalf("AuthorizeRequest() grant = %#v", grant)
				}
			} else if grant != (Grant{}) {
				t.Fatalf("AuthorizeRequest() denied grant = %#v, want zero", grant)
			}
		})
	}
}

func TestAuthorizeRequestPreservesLookupOrderAndMembershipArgumentOrder(t *testing.T) {
	t.Parallel()

	sequence := make([]string, 0, 2)
	users := &fakeCurrentUserReader{
		user: auth.User{ID: "actor-user"},
		onCall: func(cookie string) {
			sequence = append(sequence, "auth:"+cookie)
		},
	}
	memberships := &fakeMembershipReader{
		role: projects.RoleViewer,
		onCall: func(actorUserID, projectID string) {
			sequence = append(sequence, "membership:"+actorUserID+":"+projectID)
		},
	}
	authorizer := NewAuthorizer(users, memberships)

	_, decision, err := authorizer.AuthorizeRequest(t.Context(), requestWithSessionCookie("session-value"), "project-a", governance.SurfaceChatRead, governance.ActionRead)
	if err != nil || decision != DecisionAllowed {
		t.Fatalf("AuthorizeRequest() = (%q, %v), want allowed", decision, err)
	}
	want := []string{"auth:session-value", "membership:actor-user:project-a"}
	if len(sequence) != len(want) {
		t.Fatalf("lookup sequence = %v, want %v", sequence, want)
	}
	for i := range want {
		if sequence[i] != want[i] {
			t.Fatalf("lookup sequence = %v, want %v", sequence, want)
		}
	}
}

func TestAuthorizeRequestOperationalErrorsAreOpaque(t *testing.T) {
	t.Parallel()

	secretErr := errors.New("database password and internal row state")
	tests := []struct {
		name       string
		userErr    error
		membership error
	}{
		{name: "session store", userErr: secretErr},
		{name: "membership store", membership: secretErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authorizer := NewAuthorizer(
				&fakeCurrentUserReader{user: auth.User{ID: "actor"}, err: tt.userErr},
				&fakeMembershipReader{role: projects.RoleViewer, err: tt.membership},
			)
			_, decision, err := authorizer.AuthorizeRequest(t.Context(), requestWithSessionCookie("session"), "project-a", governance.SurfaceChatRead, governance.ActionRead)
			if !errors.Is(err, ErrAuthorizationUnavailable) {
				t.Fatalf("AuthorizeRequest() error = %v, want opaque unavailable", err)
			}
			if errors.Is(err, secretErr) || err.Error() == secretErr.Error() {
				t.Fatalf("AuthorizeRequest() exposed underlying error: %v", err)
			}
			if decision != "" {
				t.Fatalf("AuthorizeRequest() decision = %q, want empty operational outcome", decision)
			}
		})
	}
}

func requestWithSessionCookie(value string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if value != "" {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: value})
	}
	return request
}

type fakeCurrentUserReader struct {
	user   auth.User
	err    error
	calls  int
	onCall func(string)
}

func (f *fakeCurrentUserReader) CurrentUser(_ context.Context, cookie string) (auth.User, error) {
	f.calls++
	if f.onCall != nil {
		f.onCall(cookie)
	}
	return f.user, f.err
}

type fakeMembershipReader struct {
	role   projects.Role
	err    error
	calls  int
	onCall func(string, string)
}

func (f *fakeMembershipReader) ActiveProjectRole(_ context.Context, actorUserID, projectID string) (projects.Role, error) {
	f.calls++
	if f.onCall != nil {
		f.onCall(actorUserID, projectID)
	}
	return f.role, f.err
}

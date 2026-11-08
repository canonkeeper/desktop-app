package projectaccess

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
)

var (
	ErrInvalidPolicy            = errors.New("projectaccess: invalid server policy")
	ErrAuthorizationUnavailable = errors.New("projectaccess: authorization unavailable")
)

type CurrentUserReader interface {
	CurrentUser(context.Context, string) (auth.User, error)
}

// MembershipReader получает сначала ID авторизованного пользователя, затем ID
// проекта. Реализация должна скрывать неактивное участие и удалённый проект за
// ошибкой projects.ErrNotFound.
type MembershipReader interface {
	ActiveProjectRole(context.Context, string, string) (projects.Role, error)
}

type Decision string

const (
	DecisionUnauthenticated Decision = "unauthenticated"
	DecisionNotFound        Decision = "not_found"
	DecisionForbidden       Decision = "forbidden"
	DecisionAllowed         Decision = "allowed"
)

type Grant struct {
	User auth.User
	Role projects.Role
}

type Authorizer struct {
	currentUsers CurrentUserReader
	memberships  MembershipReader
}

func NewAuthorizer(currentUsers CurrentUserReader, memberships MembershipReader) Authorizer {
	return Authorizer{currentUsers: currentUsers, memberships: memberships}
}

func (a Authorizer) AuthorizeRequest(
	ctx context.Context,
	request *http.Request,
	projectID string,
	surface governance.Surface,
	action governance.Action,
) (Grant, Decision, error) {
	policy, err := ordinaryUserPolicy(surface, action)
	if err != nil {
		return Grant{}, "", err
	}
	if request == nil || a.currentUsers == nil || a.memberships == nil {
		return Grant{}, "", ErrAuthorizationUnavailable
	}

	user, decision, err := authenticate(ctx, request, a.currentUsers)
	if err != nil || decision != DecisionAllowed {
		return Grant{}, decision, err
	}
	if !validOpaqueID(projectID) {
		return Grant{}, DecisionNotFound, nil
	}

	role, err := a.memberships.ActiveProjectRole(ctx, user.ID, projectID)
	if errors.Is(err, projects.ErrNotFound) {
		return Grant{}, DecisionNotFound, nil
	}
	if err != nil {
		return Grant{}, "", ErrAuthorizationUnavailable
	}
	if !roleSatisfiesPolicy(role, policy) {
		return Grant{}, DecisionForbidden, nil
	}

	return Grant{User: user, Role: role}, DecisionAllowed, nil
}

func authenticate(ctx context.Context, request *http.Request, currentUsers CurrentUserReader) (auth.User, Decision, error) {
	cookieValue := ""
	if cookie, err := request.Cookie(auth.SessionCookieName); err == nil {
		cookieValue = cookie.Value
	}
	user, err := currentUsers.CurrentUser(ctx, cookieValue)
	if errors.Is(err, auth.ErrUnauthenticated) || (err == nil && user.ID == "") {
		return auth.User{}, DecisionUnauthenticated, nil
	}
	if err != nil {
		return auth.User{}, "", ErrAuthorizationUnavailable
	}
	return user, DecisionAllowed, nil
}

func ordinaryUserPolicy(surface governance.Surface, action governance.Action) (governance.AccessPolicy, error) {
	var registration governance.SurfaceRegistration
	found := false
	for _, candidate := range governance.AuthorizationSurfacePolicy() {
		if candidate.Surface == surface {
			registration = candidate
			found = true
			break
		}
	}
	if !found || registration.Policy.Exception != governance.ExceptionNone {
		return "", ErrInvalidPolicy
	}

	var policy governance.AccessPolicy
	switch action {
	case governance.ActionRead:
		policy = registration.Policy.Read
	case governance.ActionMutate:
		policy = registration.Policy.Mutate
	default:
		return "", ErrInvalidPolicy
	}
	if !policy.IsUserSessionPolicy() {
		return "", ErrInvalidPolicy
	}
	return policy, nil
}

func roleSatisfiesPolicy(role projects.Role, policy governance.AccessPolicy) bool {
	switch policy {
	case governance.AccessViewer:
		return projects.CanReadProject(role)
	case governance.AccessEditor:
		return projects.CanMutateProject(role)
	case governance.AccessAdmin:
		return projects.CanInspectSupport(role)
	case governance.AccessAdminOrOwner:
		return projects.CanDeleteProject(role)
	default:
		return false
	}
}

func validOpaqueID(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

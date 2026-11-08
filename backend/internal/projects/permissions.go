package projects

import "canonkeeper/backend/internal/governance"

func CanReadProject(role Role) bool {
	switch role {
	case RoleViewer, RoleEditor, RoleAdmin, RoleOwner:
		return true
	default:
		return false
	}
}

func CanMutateProject(role Role) bool {
	switch role {
	case RoleEditor, RoleAdmin, RoleOwner:
		return true
	default:
		return false
	}
}

func CanManageMembers(role Role) bool {
	switch role {
	case RoleAdmin, RoleOwner:
		return true
	default:
		return false
	}
}

func CanAssignMemberRole(role Role) bool {
	switch role {
	case RoleViewer, RoleEditor, RoleAdmin:
		return true
	default:
		return false
	}
}

func CanDeleteProject(role Role) bool {
	switch role {
	case RoleAdmin, RoleOwner:
		return true
	default:
		return false
	}
}

func CanInspectSupport(role Role) bool {
	return CanManageMembers(role)
}

func CanRestoreBookChapter(role Role) bool {
	return CanMutateProject(role)
}

func FromGovernanceProjectRole(role governance.ProjectRole) (Role, bool) {
	switch role {
	case governance.ProjectRoleViewer:
		return RoleViewer, true
	case governance.ProjectRoleEditor:
		return RoleEditor, true
	case governance.ProjectRoleAdmin:
		return RoleAdmin, true
	case governance.ProjectRoleOwner:
		return RoleOwner, true
	default:
		return "", false
	}
}

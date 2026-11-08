package projects

import (
	"testing"

	"canonkeeper/backend/internal/governance"
)

func TestCanReadProject(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want bool
	}{
		{name: "viewer", role: RoleViewer, want: true},
		{name: "editor", role: RoleEditor, want: true},
		{name: "admin", role: RoleAdmin, want: true},
		{name: "owner", role: RoleOwner, want: true},
		{name: "unknown", role: Role("maintainer"), want: false},
		{name: "empty", role: Role(""), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanReadProject(tt.role); got != tt.want {
				t.Fatalf("CanReadProject(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestCanMutateProject(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want bool
	}{
		{name: "viewer", role: RoleViewer, want: false},
		{name: "editor", role: RoleEditor, want: true},
		{name: "admin", role: RoleAdmin, want: true},
		{name: "owner", role: RoleOwner, want: true},
		{name: "unknown", role: Role("maintainer"), want: false},
		{name: "empty", role: Role(""), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanMutateProject(tt.role); got != tt.want {
				t.Fatalf("CanMutateProject(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestCanManageMembers(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want bool
	}{
		{name: "viewer", role: RoleViewer, want: false},
		{name: "editor", role: RoleEditor, want: false},
		{name: "admin", role: RoleAdmin, want: true},
		{name: "owner", role: RoleOwner, want: true},
		{name: "unknown", role: Role("maintainer"), want: false},
		{name: "empty", role: Role(""), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanManageMembers(tt.role); got != tt.want {
				t.Fatalf("CanManageMembers(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestCanDeleteProject(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want bool
	}{
		{name: "viewer", role: RoleViewer, want: false},
		{name: "editor", role: RoleEditor, want: false},
		{name: "admin", role: RoleAdmin, want: true},
		{name: "owner", role: RoleOwner, want: true},
		{name: "unknown", role: Role("maintainer"), want: false},
		{name: "empty", role: Role(""), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanDeleteProject(tt.role); got != tt.want {
				t.Fatalf("CanDeleteProject(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestCanUseDerivedCapabilities(t *testing.T) {
	tests := []struct {
		name               string
		role               Role
		inspectSupport     bool
		restoreBookChapter bool
	}{
		{name: "viewer", role: RoleViewer},
		{name: "editor", role: RoleEditor, restoreBookChapter: true},
		{name: "admin", role: RoleAdmin, inspectSupport: true, restoreBookChapter: true},
		{name: "owner", role: RoleOwner, inspectSupport: true, restoreBookChapter: true},
		{name: "invalid", role: Role("maintainer")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPermission(t, "CanInspectSupport", CanInspectSupport(tt.role), tt.inspectSupport)
			assertPermission(t, "CanRestoreBookChapter", CanRestoreBookChapter(tt.role), tt.restoreBookChapter)
		})
	}
}

func TestGovernanceProjectRoleConversionIsExact(t *testing.T) {
	tests := []struct {
		projectRole    Role
		governanceRole governance.ProjectRole
	}{
		{projectRole: RoleViewer, governanceRole: governance.ProjectRoleViewer},
		{projectRole: RoleEditor, governanceRole: governance.ProjectRoleEditor},
		{projectRole: RoleAdmin, governanceRole: governance.ProjectRoleAdmin},
		{projectRole: RoleOwner, governanceRole: governance.ProjectRoleOwner},
	}
	for _, tt := range tests {
		gotProject, ok := FromGovernanceProjectRole(tt.governanceRole)
		if !ok || gotProject != tt.projectRole {
			t.Fatalf("FromGovernanceProjectRole(%q) = %q, %v", tt.governanceRole, gotProject, ok)
		}
	}

	if got, ok := FromGovernanceProjectRole(governance.ProjectRole("maintainer")); ok || got != "" {
		t.Fatalf("FromGovernanceProjectRole(invalid) = %q, %v", got, ok)
	}
}

func assertPermission(t *testing.T, name string, got, want bool) {
	t.Helper()
	if got != want {
		t.Fatalf("%s() = %v, want %v", name, got, want)
	}
}

package projectaccess

import "canonkeeper/backend/internal/projects"

type Visibility string

const (
	VisibilityActive        Visibility = "active"
	VisibilityDeletedSource Visibility = "deleted_source"
)

// ResourceIdentity — закрытый набор владельцев, которые возвращают actor-scoped
// резолверы книги и главы.
type ResourceIdentity interface {
	resourceIdentity()
}

type BookIdentity struct{ BookID string }

func (BookIdentity) resourceIdentity() {}

type ChapterIdentity struct {
	BookID    string
	ChapterID string
}

func (ChapterIdentity) resourceIdentity() {}

type ResolvedResourceAccess struct {
	ProjectID   string
	Identity    ResourceIdentity
	ProjectRole projects.Role
	Visibility  Visibility
}

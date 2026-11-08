package projectstore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

const activeProjectRoleForActorSQL = `
SELECT m.role
FROM project_memberships m
JOIN projects p ON p.id = m.project_id
WHERE m.user_id = $1
  AND m.project_id = $2
  AND m.removed_at IS NULL
  AND p.deleted_at IS NULL`

// ActiveProjectRole возвращает только активную роль авторизованного пользователя
// в указанном активном проекте. ID пользователя намеренно идёт первым, чтобы
// соответствовать projectaccess.MembershipReader.
func (s *Store) ActiveProjectRole(ctx context.Context, actorUserID, projectID string) (projects.Role, error) {
	actorUUID, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return "", projects.ErrNotFound
	}
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return "", projects.ErrNotFound
	}

	var role string
	err = s.Pool.QueryRow(ctx, activeProjectRoleForActorSQL, actorUUID, projectUUID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", projects.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return projects.Role(role), nil
}

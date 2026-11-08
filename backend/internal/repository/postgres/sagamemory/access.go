package sagamemory

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/services/sagareference"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// ReadableView checks that the actor may read a text version of the project: any active member
// reads the published version, the draft needs an editor or above — the rule of the indexing
// status. A project the actor is not an active member of is not found.
func (repository *Repository) ReadableView(ctx context.Context, actorUserID, projectID, view string) error {
	actor, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return sagareference.ErrNotFound
	}
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return sagareference.ErrNotFound
	}
	value, err := sqlc.New(repository.db.Pool).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{
		ActorUserID: actor, ProjectID: project,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sagareference.ErrNotFound
	}
	if err != nil {
		return err
	}
	role := projects.Role(value)
	switch {
	case !projects.CanReadProject(role):
		return sagareference.ErrNotFound
	case view == "draft" && !projects.CanMutateProject(role):
		return sagareference.ErrDenied
	default:
		return nil
	}
}

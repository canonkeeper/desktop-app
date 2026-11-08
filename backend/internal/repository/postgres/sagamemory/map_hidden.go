package sagamemory

import (
	"context"
	"errors"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/services/sagareference"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// MapHiddenItems reads what the author took off the map of a project (specs/2026-11-04-saga-map.md, R11.3), newest first.
func (repository *Repository) MapHiddenItems(ctx context.Context, projectID string) ([]domain.MapHiddenItem, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, sagareference.ErrNotFound
	}
	rows, err := sqlc.New(repository.db.Pool).ListSagaMapHidden(ctx, project)
	if err != nil {
		return nil, err
	}
	items := make([]domain.MapHiddenItem, len(rows))
	for i, row := range rows {
		items[i] = domain.MapHiddenItem{
			ID: storedb.UUIDString(row.ID), NodeKey: row.NodeKey, OtherKey: row.OtherKey, NodeName: row.NodeName, OtherName: row.OtherName, CreatedAt: row.CreatedAt.Time,
		}
	}
	return items, nil
}

// mapWriteTx runs a decision under the write proof of the session, for an editor or above.
func (repository *Repository) mapWriteTx(ctx context.Context, write sagareference.MapWrite, run func(*sqlc.Queries, pgtype.UUID) error) error {
	actor, err := storedb.ParseUUID(write.ActorUserID)
	if err != nil {
		return sagareference.ErrNotFound
	}
	project, err := storedb.ParseUUID(write.ProjectID)
	if err != nil {
		return sagareference.ErrNotFound
	}
	return repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := authstore.LockSessionWriteProof(ctx, tx, actor, write.Proof); err != nil {
			return err
		}
		queries := sqlc.New(tx)
		role, err := queries.GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{ActorUserID: actor, ProjectID: project})
		if errors.Is(err, pgx.ErrNoRows) {
			return sagareference.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !projects.CanMutateProject(projects.Role(role)) {
			return sagareference.ErrDenied
		}
		return run(queries, project)
	})
}

// HideMapItem keeps a decision; the same one again is that decision.
func (repository *Repository) HideMapItem(ctx context.Context, write sagareference.MapWrite, item domain.MapHiddenItem) (domain.MapHiddenItem, error) {
	var kept domain.MapHiddenItem
	err := repository.mapWriteTx(ctx, write, func(queries *sqlc.Queries, project pgtype.UUID) error {
		row, err := queries.HideSagaMapItem(ctx, sqlc.HideSagaMapItemParams{
			ProjectID: project, NodeKey: item.NodeKey, OtherKey: item.OtherKey, NodeName: item.NodeName, OtherName: item.OtherName,
		})
		if err != nil {
			return err
		}
		kept = domain.MapHiddenItem{
			ID: storedb.UUIDString(row.ID), NodeKey: row.NodeKey, OtherKey: row.OtherKey, NodeName: row.NodeName, OtherName: row.OtherName, CreatedAt: row.CreatedAt.Time,
		}
		return nil
	})
	return kept, err
}

// RestoreMapItem removes a decision; one already gone is not found.
func (repository *Repository) RestoreMapItem(ctx context.Context, write sagareference.MapWrite, id string) error {
	hidden, err := storedb.ParseUUID(id)
	if err != nil {
		return sagareference.ErrNotFound
	}
	return repository.mapWriteTx(ctx, write, func(queries *sqlc.Queries, project pgtype.UUID) error {
		removed, err := queries.RestoreSagaMapItem(ctx, sqlc.RestoreSagaMapItemParams{ProjectID: project, ID: hidden})
		if err != nil {
			return err
		}
		if removed == 0 {
			return sagareference.ErrNotFound
		}
		return nil
	})
}

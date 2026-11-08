package canonstore

import (
	"context"
	"errors"
	"math"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (db *Store) CanReadCanonTextIndexTarget(
	ctx context.Context,
	actorIDValue string,
	subject jobs.CanonTextIndexSubject,
) (bool, error) {
	if db == nil || db.Pool == nil || subject.Type != jobs.KindCanonTextIndex ||
		subject.CatalogRevision == 0 || subject.CatalogRevision > math.MaxInt64 ||
		!validSourceCatalogView(subject.SourceView) {
		return false, nil
	}
	actorID, projectID, ok := canonTargetIDs(actorIDValue, subject.ProjectID)
	if !ok {
		return false, nil
	}
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	role, revision, err := currentCanonTargetRole(ctx, tx, actorID, projectID, subject.SourceView)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	allowed := canonTargetRoleAllows(role, subject.SourceView) && revision == int64(subject.CatalogRevision)
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return allowed, nil
}

// CanReadChapterCardsTarget lets an actor see the cards Job of a text version they can read.
func (db *Store) CanReadChapterCardsTarget(
	ctx context.Context,
	actorIDValue string,
	subject jobs.ChapterCardsSubject,
) (bool, error) {
	if db == nil || db.Pool == nil || subject.Type != jobs.KindChapterCards || !validSourceCatalogView(subject.SourceView) {
		return false, nil
	}
	actorID, projectID, ok := canonTargetIDs(actorIDValue, subject.ProjectID)
	if !ok {
		return false, nil
	}
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	role, _, err := currentCanonTargetRole(ctx, tx, actorID, projectID, subject.SourceView)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	allowed := canonTargetRoleAllows(role, subject.SourceView)
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return allowed, nil
}

func canonTargetIDs(actorIDValue, projectIDValue string) (pgtype.UUID, pgtype.UUID, bool) {
	actorID, err := storedb.ParseUUID(actorIDValue)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	projectID, err := storedb.ParseUUID(projectIDValue)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	return actorID, projectID, true
}

func currentCanonTargetRole(
	ctx context.Context,
	tx pgx.Tx,
	actorID pgtype.UUID,
	projectID pgtype.UUID,
	view governance.SourceView,
) (projects.Role, int64, error) {
	var role string
	var revision int64
	err := tx.QueryRow(ctx, `
		SELECT membership.role, marker.catalog_revision
		FROM projects AS project
		JOIN project_memberships AS membership
		  ON membership.project_id = project.id
		 AND membership.user_id = $2
		 AND membership.removed_at IS NULL
		JOIN project_source_views AS marker
		  ON marker.project_id = project.id
		 AND marker.project_lifecycle_generation = project.lifecycle_generation
		 AND marker.source_view = $3
		WHERE project.id = $1 AND project.deleted_at IS NULL
	`, projectID, actorID, string(view)).Scan(&role, &revision)
	return projects.Role(role), revision, err
}

func canonTargetRoleAllows(role projects.Role, view governance.SourceView) bool {
	if view == governance.SourceViewDraft {
		return projects.CanMutateProject(role)
	}
	return view == governance.SourceViewPublished && projects.CanReadProject(role)
}

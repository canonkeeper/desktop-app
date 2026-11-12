package indexing

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/domain/canon"
	indexingdomain "canonkeeper/backend/internal/domain/indexing"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
	indexingservice "canonkeeper/backend/internal/services/indexing"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (store *Store) GetStatus(ctx context.Context, input indexingservice.GetStatusInput) (canon.IndexingStatusResponse, error) {
	if store == nil || store.db == nil || store.db.Pool == nil || ctx == nil {
		return canon.IndexingStatusResponse{}, ErrInvalid
	}
	actorID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return canon.IndexingStatusResponse{}, indexingdomain.ErrNotFound
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return canon.IndexingStatusResponse{}, indexingdomain.ErrNotFound
	}
	scope := statusScope{bookID: input.BookID, sourceView: governance.SourceView(input.SourceView)}
	if scope.bookID != "" {
		bookID, parseErr := storedb.ParseUUID(scope.bookID)
		if parseErr != nil {
			return canon.IndexingStatusResponse{}, indexingdomain.ErrNotFound
		}
		scope.bookID = storedb.UUIDString(bookID)
	}
	tx, err := store.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return canon.IndexingStatusResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := loadStatusSnapshot(ctx, tx, actorID, projectID, scope.bookID)
	if err != nil {
		return canon.IndexingStatusResponse{}, err
	}
	snapshot.waiting = input.Waiting
	response, err := buildStatusResponse(snapshot, scope)
	if err != nil {
		return canon.IndexingStatusResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return canon.IndexingStatusResponse{}, err
	}
	return response, nil
}

// loadStatusSnapshot reads the actor's role, the current text versions with their books in
// order, per book how many chapters have current text and current cards, and the Jobs the
// projection picks from: the latest readable Job of each of its groups, the import of bookID
// among them (specs/2026-10-28-fast-job-list-and-postgres.md, UC3).
func loadStatusSnapshot(ctx context.Context, tx pgx.Tx, actorID, projectID pgtype.UUID, bookID string) (statusSnapshot, error) {
	queries := sqlc.New(tx)
	roleValue, err := queries.GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{
		ProjectID: projectID, ActorUserID: actorID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return statusSnapshot{}, indexingdomain.ErrNotFound
	}
	if err != nil {
		return statusSnapshot{}, err
	}
	role := projects.Role(roleValue)
	if !projects.CanReadProject(role) {
		return statusSnapshot{}, indexingdomain.ErrNotFound
	}
	views, err := loadStatusViews(ctx, tx, projectID)
	if err != nil {
		return statusSnapshot{}, err
	}
	wave, err := loadStatusWave(ctx, tx, projectID, actorID)
	if err != nil {
		return statusSnapshot{}, err
	}
	snapshot := statusSnapshot{role: role, views: views, wave: wave}
	jobRows, err := queries.ListIndexingStatusJobs(ctx, sqlc.ListIndexingStatusJobsParams{
		ProjectID: projectID, ActorUserID: actorID, BookID: bookID,
	})
	if err != nil {
		return statusSnapshot{}, err
	}
	for _, row := range jobRows {
		job, err := jobstore.JobFromSQLJob(row.Job)
		if err != nil {
			return statusSnapshot{}, err
		}
		snapshot.jobs = append(snapshot.jobs, job)
	}
	return snapshot, nil
}

// loadStatusViews reads the current text versions of a live project with their books in order and, per book, how many
// chapters have current text and current cards. The wave of «Сага готова» closes on the same counters the status shows.
func loadStatusViews(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) (map[governance.SourceView]statusViewSnapshot, error) {
	views := make(map[governance.SourceView]statusViewSnapshot)
	rows, err := tx.Query(ctx, `
		SELECT marker.source_view, marker.catalog_revision
		FROM project_source_views AS marker
		JOIN projects AS project
		  ON project.id=marker.project_id
		 AND project.lifecycle_generation=marker.project_lifecycle_generation
		 AND project.deleted_at IS NULL
		WHERE marker.project_id=$1
		ORDER BY marker.source_view
	`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var viewValue string
		var revision int64
		if err := rows.Scan(&viewValue, &revision); err != nil {
			rows.Close()
			return nil, err
		}
		view := governance.SourceView(viewValue)
		if revision < 1 || view != governance.SourceViewPublished && view != governance.SourceViewDraft {
			rows.Close()
			return nil, indexingdomain.ErrConflict
		}
		views[view] = statusViewSnapshot{revision: uint64(revision)}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `
		SELECT member.source_view, member.book_id,
		  count(chapter_member.chapter_id)::integer,
		  count(state.chapter_id)::integer,
		  count(card.chapter_id)::integer
		FROM book_source_members AS member
		JOIN project_source_views AS marker
		  ON marker.project_id=member.project_id AND marker.source_view=member.source_view
		 AND marker.project_lifecycle_generation=member.project_lifecycle_generation
		JOIN books AS book
		  ON book.id=member.book_id AND book.project_id=member.project_id
		 AND book.lifecycle_generation=member.book_lifecycle_generation AND book.deleted_at IS NULL
		LEFT JOIN chapter_source_members AS chapter_member
		  ON chapter_member.project_id=member.project_id AND chapter_member.source_view=member.source_view
		 AND chapter_member.project_lifecycle_generation=member.project_lifecycle_generation
		 AND chapter_member.book_id=member.book_id
		 AND chapter_member.book_lifecycle_generation=member.book_lifecycle_generation
		 AND EXISTS (
		   SELECT 1 FROM chapters AS chapter
		   WHERE chapter.id=chapter_member.chapter_id AND chapter.project_id=chapter_member.project_id
		     AND chapter.lifecycle_generation=chapter_member.chapter_lifecycle_generation AND chapter.deleted_at IS NULL
		 )
		LEFT JOIN chapter_text_index_state AS state
		  ON state.project_id=chapter_member.project_id AND state.source_view=chapter_member.source_view
		 AND state.chapter_id=chapter_member.chapter_id AND state.source_revision=chapter_member.source_revision
		LEFT JOIN chapter_cards AS card
		  ON card.project_id=state.project_id AND card.chapter_id=state.chapter_id
		 AND card.content_hash=state.chapter_content_hash
		WHERE member.project_id=$1
		GROUP BY member.source_view, member.book_id, book."order"
		ORDER BY member.source_view, book."order", member.book_id
	`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var viewValue string
		var bookID pgtype.UUID
		var book statusBookSnapshot
		if err := rows.Scan(&viewValue, &bookID, &book.chapters, &book.indexed, &book.carded); err != nil {
			rows.Close()
			return nil, err
		}
		view := governance.SourceView(viewValue)
		viewSnapshot, ok := views[view]
		if !ok {
			rows.Close()
			return nil, indexingdomain.ErrConflict
		}
		book.id = storedb.UUIDString(bookID)
		viewSnapshot.books = append(viewSnapshot.books, book)
		views[view] = viewSnapshot
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return views, nil
}

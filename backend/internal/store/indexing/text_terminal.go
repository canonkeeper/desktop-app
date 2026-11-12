package indexingstore

import (
	"context"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) CompleteCanonTextIndexForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.CanonTextIndexSubject) error {
	return s.terminalizeCanonTextIndexForClaim(ctx, claim, subject, jobs.StatusRunning, jobs.StatusSucceeded, nil, true)
}

func (s *Store) CancelCanonTextIndexForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.CanonTextIndexSubject) error {
	return s.terminalizeCanonTextIndexForClaim(ctx, claim, subject, jobs.StatusCanceling, jobs.StatusCanceled, nil, false)
}

func (s *Store) FailCanonTextIndexForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.CanonTextIndexSubject, failure jobs.Error) error {
	return s.terminalizeCanonTextIndexForClaim(ctx, claim, subject, jobs.StatusRunning, jobs.StatusFailed, &failure, false)
}

type canonTextTerminalOwner struct {
	id                  pgtype.UUID
	sourceView          pgtype.Text
	manifestVersion     int32
	manifestHash        []byte
	refCount            int32
	sourceIdentityState string
}

func (s *Store) terminalizeCanonTextIndexForClaim(
	ctx context.Context,
	claim jobs.WorkerClaim,
	subject jobs.CanonTextIndexSubject,
	from jobs.Status,
	to jobs.Status,
	failure *jobs.Error,
	checkCatalog bool,
) error {
	projectID, jobID, err := textIndexClaimIDs(claim)
	if err != nil || s == nil || s.DB == nil || subject.ProjectID != claim.ProjectID {
		return ErrTextIndexValidation
	}
	if _, err := jobs.EncodeSubject(jobs.KindCanonTextIndex, subject); err != nil {
		return ErrTextIndexValidation
	}
	var errorJSON []byte
	if failure != nil {
		errorJSON, err = jobs.EncodeError(*failure)
		if err != nil {
			return ErrTextIndexValidation
		}
	}
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		projectGeneration, err := lockCanonTextTerminalProject(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if checkCatalog {
			if err := lockCanonTextTerminalCatalog(ctx, tx, projectID, projectGeneration, subject); err != nil {
				return err
			}
		}
		owner, err := lockCanonTextTerminalJobAndOwner(ctx, tx, projectID, jobID, claim, subject, from, to)
		if err != nil {
			return err
		}
		var resultJSON []byte
		progress := 0.0
		if to == jobs.StatusSucceeded {
			coverage, err := canonTextCoverageInTx(ctx, tx, projectID, projectGeneration, subject)
			if err != nil {
				return err
			}
			resultJSON, err = jobs.EncodeResult(jobs.KindCanonTextIndex, jobs.CanonTextIndexResult{
				Type: jobs.KindCanonTextIndex, SourceView: subject.SourceView, TextCoverage: coverage,
			})
			if err != nil {
				return ErrTextIndexConflict
			}
			if _, err := s.queueChapterCardsInTx(ctx, tx, subject.ProjectID, projectGeneration, subject.SourceView); err != nil {
				return err
			}
			if subject.SourceView == governance.SourceViewDraft {
				if err := markDraftBooksReadyInTx(ctx, tx, projectID, projectGeneration); err != nil {
					return err
				}
				if err := jobstore.CompleteReadyImportQueuesInTx(ctx, tx, projectID); err != nil {
					return err
				}
			}
			progress = 1
		}
		updated, err := tx.Exec(ctx, `
			WITH terminal_clock AS (
			  SELECT date_trunc('microseconds', clock_timestamp()) AS value
			)
			UPDATE jobs
			SET status=$1,
			    progress_json=jsonb_set(COALESCE(progress_json, '{}'::jsonb), '{value}', to_jsonb($2::double precision), true),
			    result_json=$3::jsonb, error_json=$4::jsonb,
			    claimed_at=NULL, completed_at=terminal_clock.value, updated_at=terminal_clock.value
			FROM terminal_clock
			WHERE project_id=$5 AND id=$6 AND kind='canon_text_index'
			  AND status=$7 AND claim_incarnation=$8
			  AND artifact_source_owner_id=$9
			  AND (($10='canceled' AND cancel_requested_at IS NOT NULL)
			       OR ($10<>'canceled' AND cancel_requested_at IS NULL))
		`, string(to), progress, resultJSON, errorJSON, projectID, jobID, string(from), claim.ClaimIncarnation, owner.id, string(to))
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return ErrTextIndexConflict
		}
		if err := s.hit("after_terminal_job"); err != nil {
			return err
		}
		if _, err := sqlc.New(tx).SealArtifactSourceOwner(ctx, sqlc.SealArtifactSourceOwnerParams{
			OwnerID: owner.id, ProjectID: projectID, ArtifactKind: string(governance.ArtifactJob), ArtifactID: jobID,
			SourceView: owner.sourceView, ManifestVersion: owner.manifestVersion, ManifestHash: owner.manifestHash,
			RefCount: owner.refCount, SourceIdentityState: owner.sourceIdentityState,
		}); err != nil {
			return mapTextIndexError(err)
		}
		return s.hit("after_terminal_owner_seal")
	})
	return mapTextIndexError(err)
}

// QueueChapterCards queues the cards Job of an indexed text version outside a text index
// terminal, for cards the version still lacks; a Job already waiting is reused. It reports whether a Job waits for the
// version: a build whose helper is not connected queues none (queueChapterCardsInTx).
func (s *Store) QueueChapterCards(ctx context.Context, projectID string, sourceView governance.SourceView) (bool, error) {
	id, err := storedb.ParseUUID(projectID)
	if err != nil {
		return false, ErrTextIndexValidation
	}
	queued := false
	err = mapTextIndexError(s.WithTx(ctx, func(tx pgx.Tx) error {
		projectGeneration, err := lockCanonTextTerminalProject(ctx, tx, id)
		if err != nil {
			return err
		}
		queued, err = s.queueChapterCardsInTx(ctx, tx, projectID, projectGeneration, sourceView)
		return err
	}))
	return queued && err == nil, err
}

// QueueSagaLayer queues the thread layer Job of a project built from a text version
// (specs/2026-11-01-writer-assistant-index.md, R2.1, R2.2).
func (s *Store) QueueSagaLayer(ctx context.Context, projectID string, sourceView governance.SourceView) error {
	id, err := storedb.ParseUUID(projectID)
	if err != nil {
		return ErrTextIndexValidation
	}
	return mapTextIndexError(s.WithTx(ctx, func(tx pgx.Tx) error {
		projectGeneration, err := lockCanonTextTerminalProject(ctx, tx, id)
		if err != nil {
			return err
		}
		source, err := projectJobSource(projectID, projectGeneration, sourceView)
		if err != nil {
			return err
		}
		_, err = jobstore.New(s.DB).UpsertSagaLayerIntent(ctx, tx, jobs.SagaLayerIntentInput{
			Subject: jobs.SagaLayerSubject{Type: jobs.KindSagaLayer, ProjectID: projectID, SourceView: sourceView},
			Source:  source,
		})
		return err
	}))
}

// queueChapterCardsInTx queues the cards Job of the indexed text version (UC2 step 1) and reports whether one waits. The
// one rule of a build without its helper (specs/2026-11-12-desk-window.md WN3.4): no cards Job is queued — nobody would
// read the chapters, and a Job would only end refused; the text is indexed and found all the same, and the catch-up of the
// missing cards queues it once the helper is there.
func (s *Store) queueChapterCardsInTx(
	ctx context.Context,
	tx pgx.Tx,
	projectID string,
	projectGeneration int64,
	sourceView governance.SourceView,
) (bool, error) {
	if s.helper != nil && !s.helper.Connected(ctx) {
		return false, nil
	}
	source, err := projectJobSource(projectID, projectGeneration, sourceView)
	if err != nil {
		return false, err
	}
	_, err = jobstore.New(s.DB).UpsertChapterCardsIntent(ctx, tx, jobs.ChapterCardsIntentInput{
		Subject: jobs.ChapterCardsSubject{
			Type: jobs.KindChapterCards, ProjectID: projectID, SourceView: sourceView,
		},
		Source: source,
	})
	return err == nil, mapTextIndexError(err)
}

// projectJobSource is the manifest of a system Job over a whole text version: the project at its
// generation, so a book or chapter deletion leaves the Job alone and a project deletion fences it.
func projectJobSource(projectID string, projectGeneration int64, sourceView governance.SourceView) (jobs.JobSourceManifest, error) {
	view, err := governance.PresentSourceView(sourceView)
	if err != nil {
		return jobs.JobSourceManifest{}, ErrTextIndexValidation
	}
	projectRef, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID:         projectID,
		ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
	}, view, governance.AbsentChatArm())
	if err != nil {
		return jobs.JobSourceManifest{}, ErrTextIndexValidation
	}
	return jobs.JobSourceManifest{SourceView: view, Refs: []governance.ArtifactSourceRef{projectRef}}, nil
}

// markDraftBooksReadyInTx makes every book of the indexed draft version ready: its text is
// searchable, and cards follow in the background (R2.4).
func markDraftBooksReadyInTx(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, projectGeneration int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE books AS book
		SET status='ready', updated_at=clock_timestamp()
		FROM book_source_members AS member
		WHERE member.project_id=$1 AND member.source_view='draft' AND member.project_lifecycle_generation=$2
		  AND book.id=member.book_id AND book.project_id=member.project_id
		  AND book.lifecycle_generation=member.book_lifecycle_generation
		  AND book.deleted_at IS NULL AND book.status<>'ready'
	`, projectID, projectGeneration)
	return err
}

func lockCanonTextTerminalProject(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) (int64, error) {
	var generation int64
	err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation
		FROM projects
		WHERE id=$1 AND deleted_at IS NULL AND lifecycle_generation >= 1
		FOR SHARE
	`, projectID).Scan(&generation)
	if err != nil {
		return 0, mapTextIndexError(err)
	}
	return generation, nil
}

func lockCanonTextTerminalCatalog(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
	subject jobs.CanonTextIndexSubject,
) error {
	if err := lockCanonTextTerminalRows(ctx, tx, `
		SELECT id
		FROM books
		WHERE project_id=$1 AND deleted_at IS NULL AND lifecycle_generation >= 1
		ORDER BY "order", id
		FOR SHARE
	`, projectID); err != nil {
		return err
	}
	if err := lockCanonTextTerminalRows(ctx, tx, `
		SELECT chapter.id
		FROM chapters AS chapter
		JOIN books AS book ON book.id=chapter.book_id AND book.project_id=chapter.project_id AND book.deleted_at IS NULL
		WHERE chapter.project_id=$1 AND chapter.deleted_at IS NULL AND chapter.lifecycle_generation >= 1
		ORDER BY book."order", chapter."order", chapter.id
		FOR SHARE OF chapter
	`, projectID); err != nil {
		return err
	}
	var catalogRevision int64
	if err := tx.QueryRow(ctx, `
		SELECT catalog_revision
		FROM project_source_views
		WHERE project_id=$1 AND source_view=$2 AND project_lifecycle_generation=$3 AND catalog_revision=$4
		FOR SHARE
	`, projectID, string(subject.SourceView), projectGeneration, int64(subject.CatalogRevision)).Scan(&catalogRevision); err != nil {
		return mapTextIndexError(err)
	}
	if err := validateCanonTextTerminalBookMembers(ctx, tx, projectID, projectGeneration, subject); err != nil {
		return err
	}
	return validateCanonTextTerminalChapterMembers(ctx, tx, projectID, projectGeneration, subject)
}

func lockCanonTextTerminalRows(ctx context.Context, tx pgx.Tx, query string, args ...any) error {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ignored pgtype.UUID
		if err := rows.Scan(&ignored); err != nil {
			return err
		}
	}
	return rows.Err()
}

func validateCanonTextTerminalBookMembers(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, generation int64, subject jobs.CanonTextIndexSubject) error {
	rows, err := tx.Query(ctx, `
		SELECT member.book_id, book.id
		FROM book_source_members AS member
		JOIN books AS current_book
		  ON current_book.id=member.book_id AND current_book.project_id=member.project_id
		 AND current_book.deleted_at IS NULL
		LEFT JOIN books AS book
		  ON book.id=member.book_id AND book.project_id=member.project_id
		 AND book.lifecycle_generation=member.book_lifecycle_generation AND book.deleted_at IS NULL
		WHERE member.project_id=$1 AND member.source_view=$2 AND member.project_lifecycle_generation=$3
		ORDER BY member.book_id
		FOR SHARE OF member
	`, projectID, string(subject.SourceView), generation)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var memberID, liveID pgtype.UUID
		if err := rows.Scan(&memberID, &liveID); err != nil {
			return err
		}
		if !liveID.Valid || liveID != memberID {
			return ErrTextIndexConflict
		}
	}
	return rows.Err()
}

func validateCanonTextTerminalChapterMembers(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID, generation int64, subject jobs.CanonTextIndexSubject) error {
	rows, err := tx.Query(ctx, `
		SELECT member.chapter_id, book.id, chapter.id
		FROM chapter_source_members AS member
		JOIN books AS current_book
		  ON current_book.id=member.book_id AND current_book.project_id=member.project_id
		 AND current_book.deleted_at IS NULL
		JOIN chapters AS current_chapter
		  ON current_chapter.id=member.chapter_id AND current_chapter.project_id=member.project_id
		 AND current_chapter.book_id=member.book_id AND current_chapter.deleted_at IS NULL
		LEFT JOIN books AS book
		  ON book.id=member.book_id AND book.project_id=member.project_id
		 AND book.lifecycle_generation=member.book_lifecycle_generation AND book.deleted_at IS NULL
		LEFT JOIN chapters AS chapter
		  ON chapter.id=member.chapter_id AND chapter.project_id=member.project_id AND chapter.book_id=member.book_id
		 AND chapter.lifecycle_generation=member.chapter_lifecycle_generation AND chapter.deleted_at IS NULL
		WHERE member.project_id=$1 AND member.source_view=$2 AND member.project_lifecycle_generation=$3
		ORDER BY member.book_id, member.chapter_order, member.chapter_id
		FOR SHARE OF member
	`, projectID, string(subject.SourceView), generation)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var memberID, liveBookID, liveChapterID pgtype.UUID
		if err := rows.Scan(&memberID, &liveBookID, &liveChapterID); err != nil {
			return err
		}
		if !liveBookID.Valid || !liveChapterID.Valid || liveChapterID != memberID {
			return ErrTextIndexConflict
		}
	}
	return rows.Err()
}

func lockCanonTextTerminalJobAndOwner(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	jobID pgtype.UUID,
	claim jobs.WorkerClaim,
	subject jobs.CanonTextIndexSubject,
	from jobs.Status,
	to jobs.Status,
) (canonTextTerminalOwner, error) {
	var ownerID pgtype.UUID
	err := tx.QueryRow(ctx, `
		SELECT job.artifact_source_owner_id
		FROM jobs AS job
		WHERE job.project_id=$1 AND job.id=$2 AND job.kind='canon_text_index'
		  AND job.status=$3 AND job.claim_incarnation=$4
		  AND job.governance_payload_state='active'
		  AND job.subject_json->>'projectId'=$1::text
		  AND job.subject_json->>'sourceView'=$5
		  AND (job.subject_json->>'catalogRevision')::bigint=$6
		  AND (($7='canceled' AND job.cancel_requested_at IS NOT NULL)
		       OR ($7<>'canceled' AND job.cancel_requested_at IS NULL))
		FOR UPDATE
	`, projectID, jobID, string(from), claim.ClaimIncarnation, string(subject.SourceView), int64(subject.CatalogRevision), string(to)).Scan(&ownerID)
	if err != nil {
		return canonTextTerminalOwner{}, mapTextIndexError(err)
	}
	if !ownerID.Valid {
		return canonTextTerminalOwner{}, ErrTextIndexConflict
	}
	owner := canonTextTerminalOwner{id: ownerID}
	err = tx.QueryRow(ctx, `
		SELECT source_view, manifest_version, manifest_hash, ref_count, source_identity_state
		FROM artifact_source_owners
		WHERE id=$1 AND project_id=$2 AND artifact_kind='job' AND artifact_id=$3
		  AND read_state='active' AND fenced_at IS NULL
		FOR UPDATE
	`, ownerID, projectID, jobID).Scan(
		&owner.sourceView, &owner.manifestVersion, &owner.manifestHash, &owner.refCount, &owner.sourceIdentityState,
	)
	if err != nil {
		return canonTextTerminalOwner{}, mapTextIndexError(err)
	}
	if !owner.sourceView.Valid || owner.sourceView.String != string(subject.SourceView) ||
		owner.manifestVersion != 1 || len(owner.manifestHash) != 32 || owner.refCount < 1 || owner.sourceIdentityState == "" {
		return canonTextTerminalOwner{}, ErrTextIndexConflict
	}
	return owner, nil
}

func canonTextCoverageInTx(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
	subject jobs.CanonTextIndexSubject,
) (jobs.TextCoverage, error) {
	var current, total int
	err := tx.QueryRow(ctx, `
		WITH catalog_books AS (
		  SELECT member.book_id, member.book_lifecycle_generation
		  FROM book_source_members AS member
		  JOIN books AS book
		    ON book.id=member.book_id AND book.project_id=member.project_id
		   AND book.lifecycle_generation=member.book_lifecycle_generation AND book.deleted_at IS NULL
		  WHERE member.project_id=$1 AND member.source_view=$2 AND member.project_lifecycle_generation=$3
		    AND (
		      member.source_view<>'published'
		      OR EXISTS (
		        SELECT 1
		        FROM chapter_source_members AS chapter
		        JOIN chapters AS live_chapter
		          ON live_chapter.id=chapter.chapter_id AND live_chapter.project_id=chapter.project_id
		         AND live_chapter.book_id=chapter.book_id
		         AND live_chapter.lifecycle_generation=chapter.chapter_lifecycle_generation
		         AND live_chapter.deleted_at IS NULL
		        WHERE chapter.project_id=member.project_id AND chapter.source_view=member.source_view
		          AND chapter.project_lifecycle_generation=member.project_lifecycle_generation
		          AND chapter.book_id=member.book_id
		          AND chapter.book_lifecycle_generation=member.book_lifecycle_generation
		      )
		    )
		)
		SELECT
		  count(*) FILTER (WHERE NOT EXISTS (
		    SELECT 1
		    FROM chapter_source_members AS chapter
		    JOIN chapters AS live_chapter
		      ON live_chapter.id=chapter.chapter_id AND live_chapter.project_id=chapter.project_id
		     AND live_chapter.book_id=chapter.book_id
		     AND live_chapter.lifecycle_generation=chapter.chapter_lifecycle_generation
		     AND live_chapter.deleted_at IS NULL
		    LEFT JOIN chapter_text_index_state AS state
		      ON state.project_id=chapter.project_id AND state.source_view=chapter.source_view AND state.chapter_id=chapter.chapter_id
		     AND state.source_revision=chapter.source_revision
		     AND state.locator_source_generation=ck_source_generation_digest(
		       chapter.project_id, chapter.project_lifecycle_generation,
		       chapter.book_id, chapter.book_lifecycle_generation,
		       chapter.chapter_id, chapter.chapter_lifecycle_generation
		     )
		    WHERE chapter.project_id=$1 AND chapter.source_view=$2 AND chapter.project_lifecycle_generation=$3
		      AND chapter.book_id=catalog_books.book_id
		      AND chapter.book_lifecycle_generation=catalog_books.book_lifecycle_generation
		      AND state.chapter_id IS NULL
		  ))::integer,
		  count(*)::integer
		FROM catalog_books
	`, projectID, string(subject.SourceView), projectGeneration).Scan(&current, &total)
	if err != nil {
		return jobs.TextCoverage{}, err
	}
	nonCurrent := total - current
	return jobs.TextCoverage{
		CurrentBooks: current, TotalBooks: total,
		NonCurrentBooks: nonCurrent, TotalNonCurrentBooks: nonCurrent,
	}, nil
}

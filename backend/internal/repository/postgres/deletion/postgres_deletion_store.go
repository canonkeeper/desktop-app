package deletion

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"time"

	deletion "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	deletionservice "canonkeeper/backend/internal/services/deletion"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const postgresDeletionBatchSize = 100

type postgresDeletionSelection struct {
	projectID         string
	tombstoneID       string
	kind              governance.DeletionSelectionKind
	mode              governance.PurgeMode
	fencedAt          time.Time
	projectGeneration int64
	bookID            string
	bookGeneration    int64
	chapterID         string
	chapterGeneration int64
	chatID            string
	chatEpoch         int64
}

type postgresStepRow struct {
	name             string
	order            int
	cursorKind       string
	state            string
	claimIncarnation pgtype.Int8
	claimedByDigest  []byte
	cursor           pgtype.UUID
	cursorDigest     []byte
	processedCount   int64
	startedAt        pgtype.Timestamptz
	updatedAt        pgtype.Timestamptz
	completedAt      pgtype.Timestamptz
}

type postgresMutationResult struct {
	cursor string
	count  int64
}

func (store *Store) RunPostgresDeletionStep(
	ctx context.Context,
	request deletion.TargetRequest,
	step deletion.PostgresStep,
) (deletionservice.PostgresStepOutcome, error) {
	selection, err := postgresDeletionSelectionForRequest(request)
	if err != nil || step.Validate() != nil || store == nil || store.DB == nil || store.queries == nil {
		return "", deletion.ErrInvalidTargetRequest
	}
	wantSteps, err := deletion.PostgresStepRegistry(selection.kind, selection.mode)
	if err != nil || step.Order > len(wantSteps) || wantSteps[step.Order-1] != step {
		return "", deletion.ErrInvalidTargetRequest
	}

	outcome := deletionservice.PostgresStepSucceeded
	err = store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockFilesystemTargetRequest(
			ctx,
			store.queries.WithTx(tx),
			request,
			mustUUID(selection.projectID),
			mustUUID(selection.tombstoneID),
		); err != nil {
			return err
		}
		row, err := lockPostgresStep(ctx, tx, selection, step)
		if err != nil {
			return err
		}
		if row.state == "succeeded" {
			if len(row.cursorDigest) != sha256.Size || row.cursor.Valid || !row.completedAt.Valid {
				return deletionservice.ErrPostgresTargetConflict
			}
			if step.Table != "jobs" || selection.mode != governance.PurgeModeSourceHardPurge ||
				selection.kind != governance.DeletionSelectionProject {
				outcome = deletionservice.PostgresStepAlreadySucceeded
				return nil
			}
		}
		if row.state != "pending" && row.state != "running" && row.state != "succeeded" {
			return deletionservice.ErrPostgresTargetConflict
		}
		var earlierIncomplete bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM deletion_target_step_progress
			  WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
			    AND step_order < $3 AND state <> 'succeeded'
			)
		`, selection.tombstoneID, selection.projectID, step.Order).Scan(&earlierIncomplete); err != nil {
			return err
		}
		if earlierIncomplete {
			return deletionservice.ErrPostgresTargetConflict
		}

		now := time.Now().UTC().Truncate(time.Microsecond)
		if _, err := tx.Exec(ctx, `
			UPDATE deletion_target_step_progress
			SET state = 'running', claim_incarnation = $4, claimed_by_digest = $5,
			    started_at = COALESCE(started_at, $6), updated_at = $6,
			    completed_at = NULL, cursor_digest = NULL
			WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
			  AND step_name = $3 AND state IN ('pending', 'running', 'succeeded')
		`, selection.tombstoneID, selection.projectID, step.Name,
			int64(request.Claim.ClaimIncarnation), request.Claim.LeaseOwnerDigest[:], now); err != nil {
			return err
		}
		if err := fencePostgresSelectedOwners(ctx, tx, selection); err != nil {
			return err
		}
		if err := validatePostgresTerminalFence(ctx, tx, selection); err != nil {
			return err
		}

		mutation, err := runPostgresMutation(ctx, tx, selection, step, row.cursor)
		if err != nil {
			return fmt.Errorf("PostgreSQL deletion step %s: %w", step.Name, err)
		}
		if mutation.count < 0 || row.processedCount > math.MaxInt64-mutation.count {
			return deletionservice.ErrPostgresTargetConflict
		}
		total := row.processedCount + mutation.count
		lastCursor := row.cursor.String()
		if mutation.cursor != "" {
			lastCursor = mutation.cursor
		}
		if step.CursorKind == deletion.PostgresCursorUUID && mutation.count >= postgresDeletionBatchSize {
			if mutation.cursor == "" {
				return deletionservice.ErrPostgresTargetConflict
			}
			command, err := tx.Exec(ctx, `
				UPDATE deletion_target_step_progress
				SET cursor_uuid = $4, processed_count = $5, updated_at = $6
				WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
				  AND step_name = $3 AND state = 'running'
			`, selection.tombstoneID, selection.projectID, step.Name, mutation.cursor, total, now)
			if err != nil {
				return err
			}
			if command.RowsAffected() != 1 {
				return deletion.ErrLostTargetClaim
			}
			outcome = deletionservice.PostgresStepProgressed
			return nil
		}

		digest := postgresStepCursorDigest(selection, step, total, lastCursor)
		command, err := tx.Exec(ctx, `
			UPDATE deletion_target_step_progress
			SET state = 'succeeded', claim_incarnation = $4, claimed_by_digest = $5,
			    cursor_uuid = NULL, cursor_digest = $6, processed_count = $7,
			    updated_at = $8, completed_at = $8
			WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
			  AND step_name = $3 AND state = 'running'
		`, selection.tombstoneID, selection.projectID, step.Name,
			int64(request.Claim.ClaimIncarnation), request.Claim.LeaseOwnerDigest[:], digest[:], total, now)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return deletion.ErrLostTargetClaim
		}
		outcome = deletionservice.PostgresStepSucceeded
		return nil
	})
	if err != nil {
		return "", err
	}
	return outcome, nil
}

func (store *Store) VerifyPostgresDeletionComplete(
	ctx context.Context,
	request deletion.TargetRequest,
	steps []deletion.PostgresStep,
) error {
	selection, err := postgresDeletionSelectionForRequest(request)
	if err != nil || store == nil || store.DB == nil || store.queries == nil {
		return deletion.ErrInvalidTargetRequest
	}
	want, err := deletion.PostgresStepRegistry(selection.kind, selection.mode)
	if err != nil || len(steps) != len(want) {
		return deletion.ErrInvalidTargetRequest
	}
	for index := range want {
		if steps[index] != want[index] {
			return deletion.ErrInvalidTargetRequest
		}
	}
	return store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockFilesystemTargetRequest(
			ctx,
			store.queries.WithTx(tx),
			request,
			mustUUID(selection.projectID),
			mustUUID(selection.tombstoneID),
		); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT step_name, step_order, cursor_kind, state, cursor_uuid, cursor_digest,
			       claim_incarnation, claimed_by_digest, completed_at
			FROM deletion_target_step_progress
			WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
			ORDER BY step_order
			FOR UPDATE
		`, selection.tombstoneID, selection.projectID)
		if err != nil {
			return err
		}
		defer rows.Close()
		index := 0
		for rows.Next() {
			if index >= len(want) {
				return deletionservice.ErrPostgresTargetConflict
			}
			var name, cursorKind, state string
			var order int
			var cursor pgtype.UUID
			var cursorDigest, ownerDigest []byte
			var incarnation int64
			var completedAt pgtype.Timestamptz
			if err := rows.Scan(&name, &order, &cursorKind, &state, &cursor, &cursorDigest,
				&incarnation, &ownerDigest, &completedAt); err != nil {
				return err
			}
			expected := want[index]
			if name != expected.Name || order != expected.Order || cursorKind != string(expected.CursorKind) ||
				state != "succeeded" || cursor.Valid || len(cursorDigest) != sha256.Size ||
				incarnation < 1 || len(ownerDigest) != sha256.Size || !completedAt.Valid {
				return deletionservice.ErrPostgresTargetConflict
			}
			index++
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if index != len(want) {
			return deletionservice.ErrPostgresTargetConflict
		}
		return verifyPostgresNegativeInventory(ctx, tx, selection)
	})
}

func lockPostgresStep(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
	step deletion.PostgresStep,
) (postgresStepRow, error) {
	var row postgresStepRow
	err := tx.QueryRow(ctx, `
		SELECT step_name, step_order, cursor_kind, state, claim_incarnation,
		       claimed_by_digest, cursor_uuid, cursor_digest, processed_count,
		       started_at, updated_at, completed_at
		FROM deletion_target_step_progress
		WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
		  AND step_name = $3
		FOR UPDATE
	`, selection.tombstoneID, selection.projectID, step.Name).Scan(
		&row.name, &row.order, &row.cursorKind, &row.state, &row.claimIncarnation,
		&row.claimedByDigest, &row.cursor, &row.cursorDigest, &row.processedCount,
		&row.startedAt, &row.updatedAt, &row.completedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return postgresStepRow{}, deletionservice.ErrPostgresTargetConflict
	}
	if err != nil {
		return postgresStepRow{}, err
	}
	if row.name != step.Name || row.order != step.Order || row.cursorKind != string(step.CursorKind) ||
		row.processedCount < 0 || (step.CursorKind == deletion.PostgresCursorNone && row.cursor.Valid) {
		return postgresStepRow{}, deletionservice.ErrPostgresTargetConflict
	}
	return row, nil
}

func runPostgresMutation(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
	step deletion.PostgresStep,
	cursor pgtype.UUID,
) (postgresMutationResult, error) {
	if direct, ok := postgresDirectArtifactTables[step.Table]; ok {
		if step.Table == "jobs" {
			if err := rejectPostgresBindingOnlyModelCleanup(ctx, tx, selection, cursor); err != nil {
				return postgresMutationResult{}, err
			}
			if step.Operation == deletion.PostgresOperationDetach {
				return detachPostgresJobs(ctx, tx, selection, cursor)
			}
		}
		return deletePostgresArtifactRows(ctx, tx, selection, cursor, direct)
	}
	switch step.Table {
	case "artifact_source_owners":
		return deletePostgresOwners(ctx, tx, selection, cursor)
	case "artifact_source_refs":
		if err := assertPostgresRefsAbsent(ctx, tx, selection); err != nil {
			return postgresMutationResult{}, err
		}
		return postgresMutationResult{}, nil
	case "chapter_paragraphs", "chapters", "books", "project_invitations", "project_memberships":
		return deletePostgresSourceRows(ctx, tx, selection, cursor, step.Table)
	case "chat_sessions":
		return deletePostgresChatSessions(ctx, tx, selection)
	case "projects":
		return deletePostgresProject(ctx, tx, selection)
	case "deletion_target_tasks":
		if err := verifyPostgresNegativeInventory(ctx, tx, selection); err != nil {
			return postgresMutationResult{}, err
		}
		return postgresMutationResult{}, nil
	default:
		return postgresMutationResult{}, deletionservice.ErrPostgresTargetConflict
	}
}

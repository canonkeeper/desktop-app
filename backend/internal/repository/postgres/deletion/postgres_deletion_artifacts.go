package deletion

import (
	"context"
	"fmt"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func rejectPostgresBindingOnlyModelCleanup(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
	cursor pgtype.UUID,
) error {
	query := fmt.Sprintf(`
		SELECT EXISTS (
		  SELECT 1
		  FROM (
		    SELECT job.id
		    FROM jobs AS job
		    JOIN artifact_source_owners AS owner
		      ON owner.id = job.artifact_source_owner_id
		     AND owner.project_id = job.project_id
		    WHERE job.project_id = $1::uuid
		      AND owner.artifact_kind = 'job'
		      AND %s
		      AND job.id > COALESCE($10::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
		    ORDER BY job.id
		    LIMIT $11
		  ) AS candidate
		  JOIN job_model_bindings AS binding
		    ON binding.project_id = $1::uuid AND binding.job_id = candidate.id
		)
	`, postgresOwnerSelectionPredicate("owner"))
	var modelBindingExists bool
	if err := tx.QueryRow(ctx, query, selection.mutationArgs(cursor, postgresDeletionBatchSize)...).Scan(&modelBindingExists); err != nil {
		return err
	}
	if modelBindingExists {
		return jobs.ErrModelOwnerCleanupRequired
	}
	return nil
}

type postgresDirectArtifact struct {
	table     string
	idColumn  string
	ownerKind string
}

var postgresDirectArtifactTables = map[string]postgresDirectArtifact{
	"chat_turns":            {"chat_turns", "id", "chat_turn"},
	"agent_workflow_inputs": {"agent_workflow_inputs", "id", "agent_workflow_input"},
	"import_job_requests":   {"import_job_requests", "job_id", "import_job_request"},
	"import_uploads":        {"import_uploads", "artifact_id", "import_upload"},
	"jobs":                  {"jobs", "id", "job"},
}

func deletePostgresArtifactRows(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
	cursor pgtype.UUID,
	table postgresDirectArtifact,
) (postgresMutationResult, error) {
	ownerJoin := `JOIN artifact_source_owners owner
		    ON owner.id = artifact.artifact_source_owner_id
		   AND owner.project_id = artifact.project_id`
	scopePredicate := fmt.Sprintf("owner.artifact_kind = '%s' AND %s", table.ownerKind, postgresOwnerSelectionPredicate("owner"))
	if table.table == "jobs" && selection.mode == governance.PurgeModeSourceHardPurge &&
		selection.kind == governance.DeletionSelectionProject {
		ownerJoin = ""
		scopePredicate = `$2::text = 'project' AND $3::bigint >= 1
		      AND $4::uuid IS NULL AND $5::bigint IS NULL
		      AND $6::uuid IS NULL AND $7::bigint IS NULL
		      AND $8::uuid IS NULL AND $9::bigint IS NULL`
	}
	returning := fmt.Sprintf("artifact.%s AS cursor_id", table.idColumn)
	query := fmt.Sprintf(`
		WITH candidate AS (
		  SELECT artifact.%[2]s AS cursor_id
		  FROM %[1]s artifact
		  %[4]s
		  WHERE artifact.project_id = $1::uuid
		    AND %[5]s
		    AND artifact.%[2]s > COALESCE($10::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
		  ORDER BY artifact.%[2]s
		  LIMIT $11
		  FOR UPDATE OF artifact
		),
		deleted AS (
		  DELETE FROM %[1]s artifact
		  USING candidate
		  WHERE artifact.project_id = $1::uuid
		    AND artifact.%[2]s = candidate.cursor_id
		  RETURNING %[3]s
		)
		SELECT
		  COALESCE((SELECT cursor_id::text FROM deleted ORDER BY cursor_id DESC LIMIT 1), ''),
		  (SELECT count(*) FROM deleted)
	`, table.table, table.idColumn, returning, ownerJoin, scopePredicate)
	var result postgresMutationResult
	if err := tx.QueryRow(ctx, query, selection.mutationArgs(cursor, postgresDeletionBatchSize)...).Scan(&result.cursor, &result.count); err != nil {
		return postgresMutationResult{}, err
	}
	return result, nil
}

func detachPostgresJobs(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
	cursor pgtype.UUID,
) (postgresMutationResult, error) {
	query := fmt.Sprintf(`
		WITH candidate AS (
		  SELECT job.id AS cursor_id
		  FROM jobs job
		  JOIN artifact_source_owners owner
		    ON owner.id = job.artifact_source_owner_id
		   AND owner.project_id = job.project_id
		  WHERE job.project_id = $1::uuid
		    AND owner.artifact_kind = 'job'
		    AND %s
		    AND job.id > COALESCE($10::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
		  ORDER BY job.id
		  LIMIT $11
		  FOR UPDATE OF job
		),
		updated AS (
		  UPDATE jobs job
		  SET status = 'expired', progress_json = '{}'::jsonb, subject_json = NULL,
		      creator_user_id = NULL, can_cancel = false, result_json = NULL,
		      error_json = NULL, claimed_at = NULL, artifact_source_owner_id = NULL,
		      governance_payload_state = 'source_deleted', expires_at = clock_timestamp(),
		      updated_at = clock_timestamp()
		  FROM candidate
		  WHERE job.id = candidate.cursor_id AND job.project_id = $1::uuid
		  RETURNING job.id AS cursor_id
		)
		SELECT
		  COALESCE((SELECT cursor_id::text FROM updated ORDER BY cursor_id DESC LIMIT 1), ''),
		  (SELECT count(*) FROM updated)
	`, postgresOwnerSelectionPredicate("owner"))
	var result postgresMutationResult
	if err := tx.QueryRow(ctx, query, selection.mutationArgs(cursor, postgresDeletionBatchSize)...).Scan(
		&result.cursor, &result.count,
	); err != nil {
		return postgresMutationResult{}, err
	}
	return result, nil
}

func deletePostgresOwners(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
	cursor pgtype.UUID,
) (postgresMutationResult, error) {
	query := fmt.Sprintf(`
		WITH candidate AS (
		  SELECT owner.id AS cursor_id
		  FROM artifact_source_owners owner
		  WHERE %s
		    AND owner.id > COALESCE($10::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
		  ORDER BY owner.id
		  LIMIT $11
		  FOR UPDATE OF owner
		),
		deleted AS (
		  DELETE FROM artifact_source_owners owner
		  USING candidate
		  WHERE owner.id = candidate.cursor_id AND owner.project_id = $1::uuid
		  RETURNING owner.id AS cursor_id
		)
		SELECT
		  COALESCE((SELECT cursor_id::text FROM deleted ORDER BY cursor_id DESC LIMIT 1), ''),
		  (SELECT count(*) FROM deleted)
	`, postgresOwnerSelectionPredicate("owner"))
	var result postgresMutationResult
	if err := tx.QueryRow(ctx, query, selection.mutationArgs(cursor, postgresDeletionBatchSize)...).Scan(
		&result.cursor, &result.count,
	); err != nil {
		return postgresMutationResult{}, err
	}
	return result, nil
}

func assertPostgresRefsAbsent(ctx context.Context, tx pgx.Tx, selection postgresDeletionSelection) error {
	query := fmt.Sprintf(`
		SELECT EXISTS (
		  SELECT 1 FROM artifact_source_refs ref
		  WHERE %s
		)
	`, postgresExactRefPredicate("ref"))
	var exists bool
	if err := tx.QueryRow(ctx, query, selection.predicateArgs()...).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return deletionservice.ErrPostgresTargetConflict
	}
	return nil
}

func fencePostgresSelectedOwners(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
) error {
	readState := "source_deleted"
	if selection.mode == governance.PurgeModeChatEpoch {
		readState = "chat_deleted"
	}
	args := append(
		selection.predicateArgs(),
		selection.fencedAt,
		readState,
	)
	predicate := postgresOwnerSelectionPredicate("owner")
	var conflicting bool
	if err := tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT EXISTS (
		  SELECT 1 FROM artifact_source_owners owner
		  WHERE %s
		    AND (
		      owner.read_state NOT IN ('active', $11::text)
		      OR (owner.fenced_at IS NOT NULL AND owner.fenced_at <> $10::timestamptz)
		    )
		)
	`, predicate), args...).Scan(&conflicting); err != nil {
		return err
	}
	if conflicting {
		return deletionservice.ErrPostgresTargetConflict
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		UPDATE artifact_source_owners owner
		SET read_state = $11::text, fenced_at = $10::timestamptz,
		    updated_at = $10::timestamptz
		WHERE %s
		  AND (owner.read_state = 'active' OR owner.fenced_at IS NULL)
	`, predicate), args...); err != nil {
		return err
	}
	return nil
}

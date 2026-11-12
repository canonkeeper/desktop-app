package jobstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	governancestore "canonkeeper/backend/internal/store/governance"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrImportTerminalConflict = errors.New("store: import terminal conflict")

type ImportTerminalInTxInput struct {
	ProjectID        pgtype.UUID
	JobID            pgtype.UUID
	ClaimIncarnation int64
	From             jobs.Status
	To               jobs.Status
	ResultJSON       []byte
	ErrorJSON        []byte
	UpdatedAt        pgtype.Timestamptz
	QueuedCancel     bool
}

type FinalizeQueuedImportQueueSuffixInTxInput struct {
	ProjectID     pgtype.UUID
	QueueID       pgtype.UUID
	AfterPosition int
	UpdatedAt     pgtype.Timestamptz
}

// FinalizeQueuedImportQueueSuffixInTx cancels the volumes of a stopped queue it has not reached. A volume of a queue that
// failed gives its upload back to whoever started the queue, for the day the queue started: the queue never reached it
// through no fault of his (specs/2026-11-09-quota-accounting.md Q5.1). A queue the author canceled or whose project
// went keeps its uploads.
func FinalizeQueuedImportQueueSuffixInTx(
	ctx context.Context,
	tx pgx.Tx,
	input FinalizeQueuedImportQueueSuffixInTxInput,
) (int64, error) {
	rows, err := tx.Query(ctx, `
		SELECT item.import_job_id, queue.status = 'failed', COALESCE(job.creator_user_id::text, ''), job.created_at
		FROM import_queue_items AS item
		JOIN import_queues AS queue
		  ON queue.id=item.queue_id AND queue.project_id=item.project_id
		JOIN jobs AS job
		  ON job.id=item.import_job_id AND job.project_id=item.project_id
		WHERE item.project_id=$1 AND item.queue_id=$2 AND item.position>$3
		  AND queue.status IN ('failed', 'canceled', 'expired')
		  AND job.kind='import' AND job.status='queued'
		ORDER BY item.position, item.id
		FOR UPDATE OF job
	`, input.ProjectID, input.QueueID, input.AfterPosition)
	if err != nil {
		return 0, err
	}
	type unreached struct {
		jobID    pgtype.UUID
		failed   bool
		starter  string
		queuedAt time.Time
	}
	var volumes []unreached
	for rows.Next() {
		var volume unreached
		if err := rows.Scan(&volume.jobID, &volume.failed, &volume.starter, &volume.queuedAt); err != nil {
			rows.Close()
			return 0, err
		}
		volumes = append(volumes, volume)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, volume := range volumes {
		if err := FinalizeImportTerminalInTx(ctx, tx, ImportTerminalInTxInput{
			ProjectID: input.ProjectID, JobID: volume.jobID,
			From: jobs.StatusQueued, To: jobs.StatusCanceled,
			UpdatedAt: input.UpdatedAt, QueuedCancel: true,
		}, nil); err != nil {
			return 0, err
		}
		if !volume.failed || volume.starter == "" {
			continue
		}
		// A release reads no limit, so it needs no plan of the build.
		if err := (quotastore.Fence{}).ReleaseDailyInTx(ctx, tx, volume.starter, quota.KeyImportsPerDay, 1, volume.queuedAt); err != nil {
			return 0, err
		}
	}
	return int64(len(volumes)), nil
}

// CompleteReadyImportQueuesInTx finishes the running Import Queues of a project
// whose every volume was imported and whose books are ready after the text index; a volume
// the author deleted meanwhile no longer holds its queue open (R2.4). A volume that did not read holds it open until
// the author replaces its file or skips it; a skipped volume holds nothing (specs/2026-11-11-ux7-upload.md UP3.2).
func CompleteReadyImportQueuesInTx(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE import_queues AS queue
		SET status = 'succeeded', version = queue.version + 1,
		    updated_at = clock_timestamp(), completed_at = clock_timestamp()
		WHERE queue.project_id = $1 AND queue.status = 'running'
		  AND EXISTS (
		    SELECT 1 FROM import_queue_items AS item
		    WHERE item.queue_id = queue.id AND item.project_id = queue.project_id AND item.skipped_at IS NULL
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM import_queue_items AS item
		    LEFT JOIN jobs AS import_job
		      ON import_job.id = item.import_job_id AND import_job.project_id = item.project_id
		    LEFT JOIN books AS book
		      ON book.id = item.book_id AND book.project_id = item.project_id AND book.deleted_at IS NULL
		    WHERE item.queue_id = queue.id AND item.project_id = queue.project_id
		      AND item.skipped_at IS NULL
		      AND (import_job.status IS DISTINCT FROM 'succeeded'
		           OR (book.id IS NOT NULL AND book.status <> 'ready'))
		  )
	`, projectID)
	return err
}

func FinalizeImportTerminalInTx(
	ctx context.Context,
	tx pgx.Tx,
	input ImportTerminalInTxInput,
	hit func(string) error,
) error {
	var currentStatus string
	var jobOwnerID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT status, artifact_source_owner_id
		FROM jobs AS job
		WHERE id = $1 AND project_id = $2 AND kind = 'import'
		  AND status = $3
		  AND ($6 OR claim_incarnation = $4)
		  AND (($6 AND cancel_requested_at IS NULL)
		       OR (NOT $6 AND (($5 = 'failed' AND cancel_requested_at IS NULL)
		                       OR ($5 = 'canceled' AND cancel_requested_at IS NOT NULL))))
		  AND ck_artifact_source_owner_is_readable(
		    job.artifact_source_owner_id, job.project_id, 'job', job.id
		  )
		FOR UPDATE
	`, input.JobID, input.ProjectID, string(input.From), input.ClaimIncarnation,
		string(input.To), input.QueuedCancel).Scan(&currentStatus, &jobOwnerID); errors.Is(err, pgx.ErrNoRows) {
		return ErrImportTerminalConflict
	} else if err != nil {
		return err
	}
	var queueStatus string
	var queueID pgtype.UUID
	var queuePosition int
	if err := tx.QueryRow(ctx, `
		SELECT queue.status, queue.id, item.position
		FROM import_queues AS queue
		JOIN import_queue_items AS item
		  ON item.queue_id = queue.id AND item.project_id = queue.project_id
		WHERE item.project_id = $1 AND item.import_job_id = $2
		FOR UPDATE OF queue
	`, input.ProjectID, input.JobID).Scan(&queueStatus, &queueID, &queuePosition); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	var requestOwnerID pgtype.UUID
	var storageKey string
	if err := tx.QueryRow(ctx, `
		SELECT storage_key, artifact_source_owner_id
		FROM import_job_requests AS request
		WHERE job_id = $1 AND project_id = $2 AND status = 'pending'
		  AND ck_artifact_source_owner_is_readable(
		    request.artifact_source_owner_id, request.project_id, 'import_job_request', request.job_id
		  )
		FOR UPDATE
	`, input.JobID, input.ProjectID).Scan(&storageKey, &requestOwnerID); errors.Is(err, pgx.ErrNoRows) {
		return ErrImportTerminalConflict
	} else if err != nil {
		return err
	}

	var uploadOwnerID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT artifact_source_owner_id
		FROM import_uploads AS upload
		WHERE storage_key = $1 AND project_id = $2 AND content IS NOT NULL
		  AND ck_artifact_source_owner_is_readable(
		    upload.artifact_source_owner_id, upload.project_id, 'import_upload', upload.artifact_id
		  )
		FOR UPDATE
	`, storageKey, input.ProjectID).Scan(&uploadOwnerID); errors.Is(err, pgx.ErrNoRows) {
		return ErrImportTerminalConflict
	} else if err != nil {
		return err
	}

	if err := governancestore.LockImportArtifactOwnerSet(
		ctx, tx, input.ProjectID, jobOwnerID, requestOwnerID, uploadOwnerID,
	); err != nil {
		if errors.Is(err, governancestore.ErrArtifactSourcesConflict) ||
			errors.Is(err, governancestore.ErrArtifactSourcesValidation) {
			return ErrImportTerminalConflict
		}
		return err
	}

	terminalAt := input.UpdatedAt
	if !terminalAt.Valid {
		if err := tx.QueryRow(ctx, `SELECT date_trunc('microseconds', clock_timestamp())`).Scan(&terminalAt); err != nil {
			return err
		}
	}
	errorJSON := input.ErrorJSON
	if queueStatus == "running" && input.To == jobs.StatusFailed {
		var terminalError jobs.Error
		if err := json.Unmarshal(errorJSON, &terminalError); err != nil {
			return ErrImportTerminalConflict
		}
		// A refusal by a limit keeps its code, as a single import does (R3.3): the author sees the limit and his tier.
		if terminalError.Code != jobs.RetryExhaustedCode && !strings.HasPrefix(terminalError.Code, "quota.") {
			terminalError.Code = "unexpected_deterministic_failure"
		}
		marshaled, marshalErr := json.Marshal(terminalError)
		if marshalErr != nil {
			return marshalErr
		}
		errorJSON = marshaled
	}
	requestTag, err := tx.Exec(ctx, `
		UPDATE import_job_requests
		SET status = $1, updated_at = $2
		WHERE job_id = $3 AND project_id = $4 AND status = 'pending'
		  AND artifact_source_owner_id = $5
	`, string(input.To), terminalAt, input.JobID, input.ProjectID, requestOwnerID)
	if err != nil {
		return err
	}
	if requestTag.RowsAffected() != 1 {
		return ErrImportTerminalConflict
	}
	if hit != nil {
		if err := hit("after_import_request_terminal"); err != nil {
			return err
		}
	}
	uploadTag, err := tx.Exec(ctx, `
		UPDATE import_uploads
		SET content = NULL
		WHERE storage_key = $1 AND project_id = $2
		  AND artifact_source_owner_id = $3 AND content IS NOT NULL
	`, storageKey, input.ProjectID, uploadOwnerID)
	if err != nil {
		return err
	}
	if uploadTag.RowsAffected() != 1 {
		return ErrImportTerminalConflict
	}
	if hit != nil {
		if err := hit("after_import_upload_scrub"); err != nil {
			return err
		}
	}
	jobTag, err := tx.Exec(ctx, `
		UPDATE jobs
		SET status = $1,
		    result_json = $2::jsonb,
		    error_json = $3::jsonb,
		    cancel_requested_at = CASE WHEN $9 THEN $4 ELSE cancel_requested_at END,
		    claimed_at = NULL,
		    completed_at = $4,
		    expires_at = CASE WHEN $9 THEN NULL ELSE expires_at END,
		    updated_at = $4
		WHERE id = $5 AND project_id = $6 AND status = $7
		  AND ($9 OR claim_incarnation = $8)
		  AND artifact_source_owner_id = $10
	`, string(input.To), input.ResultJSON, errorJSON, terminalAt,
		input.JobID, input.ProjectID, currentStatus, input.ClaimIncarnation,
		input.QueuedCancel, jobOwnerID)
	if err != nil {
		return err
	}
	if jobTag.RowsAffected() != 1 {
		return ErrImportTerminalConflict
	}
	// A volume that did not read waits for the author at its place, and the queue goes on to the volumes after it
	// (specs/2026-11-11-ux7-upload.md UP3.2): only a stopped volume stops its queue.
	if queueStatus == "running" && input.To == jobs.StatusCanceled {
		queueTag, err := tx.Exec(ctx, `
			UPDATE import_queues AS queue
			SET status = $1, version = version + 1,
			    updated_at = $2, completed_at = $2
			WHERE queue.status = 'running'
			  AND EXISTS (
			    SELECT 1
			    FROM import_queue_items AS item
			    WHERE item.queue_id = queue.id
			      AND item.project_id = queue.project_id
			      AND item.import_job_id = $3
			  )
		`, string(input.To), terminalAt, input.JobID)
		if err != nil {
			return err
		}
		if queueTag.RowsAffected() != 1 {
			return ErrImportTerminalConflict
		}
		if _, err := FinalizeQueuedImportQueueSuffixInTx(ctx, tx, FinalizeQueuedImportQueueSuffixInTxInput{
			ProjectID: input.ProjectID, QueueID: queueID, AfterPosition: queuePosition, UpdatedAt: terminalAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

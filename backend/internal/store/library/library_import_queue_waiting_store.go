package librarystore

import (
	"context"
	"errors"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// waitingImportQueueVolume is a volume of a running queue that did not read and waits for the author at its place
// (specs/2026-11-11-ux7-upload.md UP3.2).
type waitingImportQueueVolume struct {
	storageKey pgtype.Text
	order      pgtype.Int4
	jobID      pgtype.UUID
}

// lockWaitingImportQueueVolumeInTx locks the queue and its volume and checks the volume waits: its import Job did not
// read the file, or its file did not pass the check, and the author has not skipped it. Any other volume — in work,
// read, skipped — is a conflict.
func lockWaitingImportQueueVolumeInTx(ctx context.Context, tx pgx.Tx, projectID, queueID, itemID pgtype.UUID) (sqlc.ImportQueue, waitingImportQueueVolume, error) {
	locked, err := lockImportQueue(ctx, tx, queueID, projectID)
	if err != nil {
		return sqlc.ImportQueue{}, waitingImportQueueVolume{}, err
	}
	if locked.Status != string(library.ImportQueueRunning) {
		return sqlc.ImportQueue{}, waitingImportQueueVolume{}, library.ErrConflict
	}
	var volume waitingImportQueueVolume
	var skipped bool
	var jobStatus, preflightStatus string
	if err := tx.QueryRow(ctx, `
		SELECT item.storage_key, item.confirmed_book_order, item.import_job_id, item.skipped_at IS NOT NULL,
		       COALESCE(job.status, ''), item.preflight_status
		FROM import_queue_items AS item
		LEFT JOIN jobs AS job ON job.id = item.import_job_id AND job.project_id = item.project_id
		WHERE item.id = $1 AND item.queue_id = $2 AND item.project_id = $3
		FOR UPDATE OF item
	`, itemID, queueID, projectID).Scan(&volume.storageKey, &volume.order, &volume.jobID, &skipped, &jobStatus, &preflightStatus); errors.Is(err, pgx.ErrNoRows) {
		return sqlc.ImportQueue{}, waitingImportQueueVolume{}, library.ErrNotFound
	} else if err != nil {
		return sqlc.ImportQueue{}, waitingImportQueueVolume{}, err
	}
	// It waits when its import Job did not read the file, or when its file did not pass the check and it has no Job.
	waits := jobStatus == string(jobs.StatusFailed) || jobStatus == string(jobs.StatusExpired) ||
		(!volume.jobID.Valid && preflightStatus == library.ImportQueueItemInvalid)
	if skipped || !waits || !volume.order.Valid {
		return sqlc.ImportQueue{}, waitingImportQueueVolume{}, library.ErrConflict
	}
	return locked, volume, nil
}

// ReplaceImportQueueItemFileForActor is «Заменить файл» of a volume that did not read: the new file goes to the same
// place and the volume gets a new import Job, which the queue takes in its turn — no other confirmation. A file that
// does not pass the check keeps the volume waiting with its reason; a file that does counts one new book and one upload,
// as any volume does at the start of its queue.
func (s *Store) ReplaceImportQueueItemFileForActor(ctx context.Context, input library.ReplaceImportQueueItemStoreInput) (library.ImportQueue, error) {
	projectID, actorID, queueID, itemID, err := parseQueueItemInput(input.ProjectID, input.ActorUserID, input.QueueID, input.ItemID)
	if err != nil {
		return library.ImportQueue{}, err
	}
	ready := input.PreflightStatus == library.ImportQueueItemReady
	if strings.TrimSpace(input.SourceFileName) == "" ||
		(input.PreflightStatus != library.ImportQueueItemReady && input.PreflightStatus != library.ImportQueueItemInvalid) ||
		strings.TrimSpace(input.IdempotencyKey) == "" || strings.TrimSpace(input.RequestMethod) == "" ||
		strings.TrimSpace(input.RequestPath) == "" || strings.TrimSpace(input.RequestFingerprint) == "" {
		return library.ImportQueue{}, library.ErrValidation
	}
	if ready && (strings.TrimSpace(input.StorageKey) == "" || strings.TrimSpace(input.MIMEType) == "" ||
		strings.TrimSpace(input.SHA256) == "" || input.SizeBytes <= 0 || int64(len(input.FileBytes)) != input.SizeBytes) {
		return library.ImportQueue{}, library.ErrValidation
	}

	var queue library.ImportQueue
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := authstore.LockVerifiedSessionWriteProof(ctx, tx, actorID, input.SessionWriteProof); err != nil {
			return err
		}
		if err := lockImportProjectActor(ctx, tx, projectID, actorID); err != nil {
			return err
		}
		_, volume, err := lockWaitingImportQueueVolumeInTx(ctx, tx, projectID, queueID, itemID)
		if err != nil {
			return err
		}
		at := time.Now().UTC().Truncate(time.Microsecond)
		if !ready {
			// The file goes nowhere: the volume keeps waiting at its place with the reason of this file.
			if _, err := tx.Exec(ctx, `
				UPDATE import_queue_items
				SET source_file_name = $4, preflight_status = 'invalid', preflight_error_json = $5::jsonb, updated_at = $6
				WHERE id = $1 AND queue_id = $2 AND project_id = $3
			`, itemID, queueID, projectID, strings.TrimSpace(input.SourceFileName), input.PreflightErrorJSON, at); err != nil {
				return err
			}
			return s.bumpImportQueueInTx(ctx, tx, projectID, queueID, at, &queue)
		}
		// The new file is one more book of the owner and one more upload of the actor, in the lock order of every path.
		if err := s.quota.CheckNewBooksInTx(ctx, tx, projectID.String(), 1, true); err != nil {
			return err
		}
		if err := s.quota.ReserveDailyInTx(ctx, tx, actorID.String(), quota.KeyImportsPerDay, 1, at); err != nil {
			return err
		}
		var projectGeneration int64
		if err := tx.QueryRow(ctx, `SELECT lifecycle_generation FROM projects WHERE id = $1 AND deleted_at IS NULL`, projectID).Scan(&projectGeneration); err != nil {
			return mapQueueNotFound(err)
		}
		var uploadArtifactID, uploadOwnerID, uploadRefID pgtype.UUID
		if err := tx.QueryRow(ctx, `SELECT gen_random_uuid(), gen_random_uuid(), gen_random_uuid()`).Scan(&uploadArtifactID, &uploadOwnerID, &uploadRefID); err != nil {
			return err
		}
		if err := s.createImportProjectArtifact(ctx, tx, projectID, projectGeneration,
			governance.ArtifactImportUpload, uploadArtifactID, uploadOwnerID, uploadRefID,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				rows, err := sqlc.New(tx).StoreImportFile(ctx, sqlc.StoreImportFileParams{
					StorageKey: input.StorageKey, ProjectID: projectID, MimeType: input.MIMEType,
					SizeBytes: input.SizeBytes, Sha256: input.SHA256, Content: input.FileBytes,
					ExpiresAt: storedb.Timestamptz(at.Add(library.ImportUploadRetention)), CreatedAt: storedb.Timestamptz(at),
					ArtifactID: uploadArtifactID, ArtifactSourceOwnerID: ownerID,
				})
				if err != nil {
					return err
				}
				if rows != 1 {
					return library.ErrNotFound
				}
				return nil
			},
		); err != nil {
			return err
		}
		sourceFileName := strings.TrimSpace(input.SourceFileName)
		if _, err := tx.Exec(ctx, `
			UPDATE import_queue_items
			SET storage_key = $4, source_file_name = $5, mime_type = $6, size_bytes = $7, sha256 = $8,
			    preflight_status = 'ready', preflight_error_json = NULL, updated_at = $9
			WHERE id = $1 AND queue_id = $2 AND project_id = $3
		`, itemID, queueID, projectID, input.StorageKey, sourceFileName, input.MIMEType, input.SizeBytes, input.SHA256, at); err != nil {
			return err
		}
		jobID, err := s.enqueueImportQueueVolumeInTx(ctx, tx, importQueueVolume{
			projectID: projectID, actorID: actorID, queueID: queueID, itemID: itemID, generation: projectGeneration,
			sourceFileName: sourceFileName, storageKey: input.StorageKey, mimeType: input.MIMEType,
			sha256: input.SHA256, sizeBytes: input.SizeBytes, order: volume.order.Int32,
		}, importQueueVolumeRequest{
			idempotencyKey: input.IdempotencyKey + ":" + itemID.String(), method: input.RequestMethod,
			path: input.RequestPath, fingerprint: input.RequestFingerprint,
		}, at)
		if err != nil {
			return err
		}
		relinked, err := tx.Exec(ctx, `
			UPDATE import_queue_items SET import_job_id = $4, updated_at = $6
			WHERE id = $1 AND queue_id = $2 AND project_id = $3 AND import_job_id IS NOT DISTINCT FROM $5
		`, itemID, queueID, projectID, jobID, volume.jobID, at)
		if err != nil {
			return err
		}
		if relinked.RowsAffected() != 1 {
			return library.ErrConflict
		}
		return s.bumpImportQueueInTx(ctx, tx, projectID, queueID, at, &queue)
	})
	if err != nil {
		if isUniqueViolation(err) {
			return library.ImportQueue{}, library.ErrConflict
		}
		return library.ImportQueue{}, mapQueueImportError(err)
	}
	return queue, nil
}

// SkipImportQueueItemForActor is «Пропустить» of a volume that did not read: the volume leaves the upload and its place
// stays empty — the places of the shelf are counted among its books (shelf.Places). The queue ends once its other
// volumes are on the shelf and read; a queue whose every volume was skipped ends as stopped.
func (s *Store) SkipImportQueueItemForActor(ctx context.Context, input library.SkipImportQueueItemStoreInput) (library.ImportQueue, error) {
	projectID, actorID, queueID, itemID, err := parseQueueItemInput(input.ProjectID, input.ActorUserID, input.QueueID, input.ItemID)
	if err != nil {
		return library.ImportQueue{}, err
	}
	var queue library.ImportQueue
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockImportProjectActor(ctx, tx, projectID, actorID); err != nil {
			return err
		}
		if _, _, err := lockWaitingImportQueueVolumeInTx(ctx, tx, projectID, queueID, itemID); err != nil {
			return err
		}
		at := time.Now().UTC().Truncate(time.Microsecond)
		if _, err := tx.Exec(ctx, `
			UPDATE import_queue_items SET skipped_at = $4, updated_at = $4
			WHERE id = $1 AND queue_id = $2 AND project_id = $3
		`, itemID, queueID, projectID, at); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE import_queues AS queue
			SET status = 'canceled', updated_at = $3, completed_at = $3
			WHERE queue.id = $1 AND queue.project_id = $2 AND queue.status = 'running'
			  AND NOT EXISTS (
			    SELECT 1 FROM import_queue_items AS item
			    WHERE item.queue_id = queue.id AND item.project_id = queue.project_id AND item.skipped_at IS NULL
			  )
		`, queueID, projectID, at); err != nil {
			return err
		}
		if err := jobstore.CompleteReadyImportQueuesInTx(ctx, tx, projectID); err != nil {
			return err
		}
		return s.bumpImportQueueInTx(ctx, tx, projectID, queueID, at, &queue)
	})
	if err != nil {
		return library.ImportQueue{}, mapQueueImportError(err)
	}
	return queue, nil
}

// bumpImportQueueInTx gives the queue its next version after a change of a volume and reads it back with its volumes.
func (s *Store) bumpImportQueueInTx(ctx context.Context, tx pgx.Tx, projectID, queueID pgtype.UUID, at time.Time, queue *library.ImportQueue) error {
	var row sqlc.ImportQueue
	if err := tx.QueryRow(ctx, `
		UPDATE import_queues SET version = version + 1, updated_at = $3
		WHERE id = $1 AND project_id = $2
		RETURNING id, project_id, created_by_user_id, idempotency_key, request_fingerprint,
		          status, version, created_at, updated_at, started_at, completed_at
	`, queueID, projectID, at).Scan(
		&row.ID, &row.ProjectID, &row.CreatedByUserID, &row.IdempotencyKey, &row.RequestFingerprint,
		&row.Status, &row.Version, &row.CreatedAt, &row.UpdatedAt, &row.StartedAt, &row.CompletedAt,
	); err != nil {
		return mapQueueNotFound(err)
	}
	*queue = importQueueFromSQL(row)
	items, err := loadImportQueueItems(ctx, tx, queueID, projectID)
	queue.Items = items
	return err
}

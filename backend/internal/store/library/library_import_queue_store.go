package librarystore

import (
	"context"
	"errors"
	"strings"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) CreateImportQueueDraftForActor(ctx context.Context, input library.CreateImportQueueDraftStoreInput) (library.ImportQueue, error) {
	projectID, actorID, err := parseQueueProjectActor(input.ProjectID, input.ActorUserID)
	if err != nil {
		return library.ImportQueue{}, err
	}
	queueID, err := optionalQueueUUID(input.ID)
	if err != nil {
		return library.ImportQueue{}, library.ErrValidation
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return library.ImportQueue{}, library.ErrValidation
	}
	if strings.TrimSpace(input.RequestFingerprint) == "" {
		return library.ImportQueue{}, library.ErrValidation
	}
	for _, item := range input.Items {
		if strings.TrimSpace(item.ClientItemID) == "" || strings.TrimSpace(item.SourceFileName) == "" || item.Position < 1 {
			return library.ImportQueue{}, library.ErrValidation
		}
	}

	var queue library.ImportQueue
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockImportProjectActor(ctx, tx, projectID, actorID); err != nil {
			return err
		}
		createdAt := time.Now().UTC().Truncate(time.Microsecond)
		queries := sqlc.New(tx)
		existing, existingErr := queries.LoadImportQueueByIdempotencyKey(ctx, sqlc.LoadImportQueueByIdempotencyKeyParams{
			ProjectID: projectID, CreatedByUserID: actorID, IdempotencyKey: input.IdempotencyKey,
		})
		if existingErr == nil {
			if existing.RequestFingerprint != input.RequestFingerprint {
				return library.ErrConflict
			}
			queue = importQueueFromSQL(existing)
			queue.Items, err = loadImportQueueItems(ctx, tx, existing.ID, projectID)
			return err
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		row, err := queries.CreateImportQueueRow(ctx, sqlc.CreateImportQueueRowParams{
			ID: queueID, ProjectID: projectID, CreatedByUserID: actorID, IdempotencyKey: input.IdempotencyKey,
			RequestFingerprint: input.RequestFingerprint, CreatedAt: storedb.Timestamptz(createdAt),
		})
		if err != nil {
			return err
		}
		queue = importQueueFromSQL(row)
		for _, item := range input.Items {
			itemID, err := optionalQueueUUID(item.ID)
			if err != nil {
				return library.ErrValidation
			}
			if _, err := queries.CreateImportQueueItemRow(ctx, sqlc.CreateImportQueueItemRowParams{
				ID: itemID, QueueID: row.ID, ProjectID: projectID,
				ClientItemID: item.ClientItemID, Position: int32(item.Position), SourceFileName: item.SourceFileName,
				DetectedBookOrder: optionalInt4(item.DetectedBookOrder), CreatedAt: storedb.Timestamptz(createdAt),
			}); err != nil {
				return err
			}
		}
		queue.Items, err = loadImportQueueItems(ctx, tx, row.ID, projectID)
		return err
	})
	if err != nil {
		if isQueueActiveUniqueViolation(err) {
			return library.ImportQueue{}, library.ErrActiveImportQueueExists
		}
		if isUniqueViolation(err) {
			return library.ImportQueue{}, library.ErrConflict
		}
		return library.ImportQueue{}, mapQueueNotFound(err)
	}
	return queue, nil
}

func (s *Store) GetImportQueueForActor(ctx context.Context, input library.GetImportQueueStoreInput) (library.ImportQueue, error) {
	projectID, actorID, queueID, err := parseQueueReadInput(input.ProjectID, input.ActorUserID, input.QueueID)
	if err != nil {
		return library.ImportQueue{}, err
	}
	row, err := sqlc.New(s.Pool).LoadImportQueueForActor(ctx, sqlc.LoadImportQueueForActorParams{
		QueueID: queueID, ProjectID: projectID, ActorUserID: actorID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return library.ImportQueue{}, library.ErrNotFound
	}
	if err != nil {
		return library.ImportQueue{}, err
	}
	items, err := loadImportQueueItems(ctx, s.Pool, queueID, projectID)
	if err != nil {
		return library.ImportQueue{}, err
	}
	queue := importQueueFromSQL(row)
	queue.Items = items
	return queue, nil
}

func (s *Store) ListImportQueuesForActor(ctx context.Context, input library.ListImportQueuesStoreInput) ([]library.ImportQueue, error) {
	projectID, actorID, err := parseQueueProjectActor(input.ProjectID, input.ActorUserID)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := sqlc.New(s.Pool).ListImportQueueRowsForActor(ctx, sqlc.ListImportQueueRowsForActorParams{
		ProjectID: projectID, ActorUserID: actorID, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	result := make([]library.ImportQueue, 0, len(rows))
	for _, row := range rows {
		queue := importQueueFromSQL(row)
		queue.Items, err = loadImportQueueItems(ctx, s.Pool, row.ID, projectID)
		if err != nil {
			return nil, err
		}
		result = append(result, queue)
	}
	return result, nil
}

func (s *Store) PutImportQueueItemForActor(ctx context.Context, input library.PutImportQueueItemStoreInput) (library.ImportQueueItem, error) {
	projectID, actorID, queueID, itemID, err := parseQueueItemInput(input.ProjectID, input.ActorUserID, input.QueueID, input.ItemID)
	if err != nil {
		return library.ImportQueueItem{}, err
	}
	content := input.FileBytes
	if strings.TrimSpace(input.StorageKey) == "" || strings.TrimSpace(input.MIMEType) == "" || strings.TrimSpace(input.SHA256) == "" || input.SizeBytes <= 0 || int64(len(content)) != input.SizeBytes {
		return library.ImportQueueItem{}, library.ErrValidation
	}
	status := input.PreflightStatus
	if status == "" {
		status = library.ImportQueueItemReady
	}
	if status != library.ImportQueueItemUploading && status != library.ImportQueueItemReady && status != library.ImportQueueItemInvalid {
		return library.ImportQueueItem{}, library.ErrValidation
	}

	var queue library.ImportQueue
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockImportProjectActor(ctx, tx, projectID, actorID); err != nil {
			return err
		}
		locked, err := lockImportQueue(ctx, tx, queueID, projectID)
		if err != nil {
			return err
		}
		if locked.Version != input.ExpectedVersion || (locked.Status != string(library.ImportQueueUploading) && locked.Status != string(library.ImportQueueDraft)) {
			return library.ErrConflict
		}
		item, err := sqlc.New(tx).LoadImportQueueItemForUpdate(ctx, sqlc.LoadImportQueueItemForUpdateParams{
			ItemID: itemID, QueueID: queueID, ProjectID: projectID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		}
		if err != nil {
			return err
		}
		sourceFileName := strings.TrimSpace(input.SourceFileName)
		if sourceFileName == "" {
			sourceFileName = item.SourceFileName
		}
		detected := input.DetectedBookOrder
		if detected == 0 && item.DetectedBookOrder.Valid {
			detected = int(item.DetectedBookOrder.Int32)
		}
		confirmed := input.ConfirmedBookOrder
		if confirmed == 0 && item.ConfirmedBookOrder.Valid {
			confirmed = int(item.ConfirmedBookOrder.Int32)
		}
		createdAt := time.Now().UTC().Truncate(time.Microsecond)
		expiresAt := createdAt.Add(library.ImportUploadRetention)
		if input.ExpiresAt != nil {
			expiresAt = input.ExpiresAt.UTC()
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
					SizeBytes: input.SizeBytes, Sha256: input.SHA256, Content: content,
					ExpiresAt: storedb.Timestamptz(expiresAt), CreatedAt: storedb.Timestamptz(createdAt),
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
		if item.StorageKey.Valid && item.StorageKey.String != input.StorageKey {
			if _, err := sqlc.New(tx).ScrubImportQueueItemUpload(ctx, sqlc.ScrubImportQueueItemUploadParams{
				ProjectID: projectID, StorageKey: item.StorageKey.String,
			}); err != nil {
				return err
			}
		}
		rows, err := sqlc.New(tx).UpdateImportQueueItemUpload(ctx, sqlc.UpdateImportQueueItemUploadParams{
			StorageKey: dbText(input.StorageKey), SourceFileName: sourceFileName, MimeType: dbText(input.MIMEType),
			SizeBytes: dbInt8(input.SizeBytes), Sha256: dbText(input.SHA256),
			DetectedBookOrder: optionalInt4(detected), ConfirmedBookOrder: optionalInt4(confirmed),
			PreflightStatus: status, PreflightErrorJson: input.PreflightErrorJSON, UpdatedAt: storedb.Timestamptz(createdAt),
			ItemID: itemID, QueueID: queueID, ProjectID: projectID,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return library.ErrNotFound
		}
		updated, err := sqlc.New(tx).SaveImportQueueVersion(ctx, sqlc.SaveImportQueueVersionParams{
			UpdatedAt: storedb.Timestamptz(createdAt), QueueID: queueID, ProjectID: projectID, ExpectedVersion: input.ExpectedVersion,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return library.ErrConflict
		}
		if err != nil {
			return err
		}
		queue = importQueueFromSQL(updated)
		queue.Items, err = loadImportQueueItems(ctx, tx, queueID, projectID)
		return err
	})
	if err != nil {
		if isUniqueViolation(err) {
			return library.ImportQueueItem{}, library.ErrConflict
		}
		return library.ImportQueueItem{}, mapQueueImportError(err)
	}
	for _, item := range queue.Items {
		if item.ID == storedb.UUIDString(itemID) {
			item.QueueVersion = queue.Version
			return item, nil
		}
	}
	return library.ImportQueueItem{}, library.ErrNotFound
}

func (s *Store) SaveImportQueueDraftForActor(ctx context.Context, input library.SaveImportQueueDraftStoreInput) (library.ImportQueue, error) {
	projectID, actorID, queueID, err := parseQueueReadInput(input.ProjectID, input.ActorUserID, input.QueueID)
	if err != nil {
		return library.ImportQueue{}, err
	}
	if input.ExpectedVersion < 1 {
		return library.ImportQueue{}, library.ErrValidation
	}
	items := input.Items
	seenIDs := make(map[string]struct{}, len(items))
	seenPositions := make(map[int]struct{}, len(items))
	for index := range items {
		itemID := items[index].ItemID
		if itemID == "" || items[index].Position < 1 {
			return library.ImportQueue{}, library.ErrValidation
		}
		if _, ok := seenIDs[itemID]; ok {
			return library.ImportQueue{}, library.ErrConflict
		}
		if _, ok := seenPositions[items[index].Position]; ok {
			return library.ImportQueue{}, library.ErrConflict
		}
		seenIDs[itemID] = struct{}{}
		seenPositions[items[index].Position] = struct{}{}
		items[index].ItemID = itemID
	}

	var queue library.ImportQueue
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockImportProjectActor(ctx, tx, projectID, actorID); err != nil {
			return err
		}
		locked, err := lockImportQueue(ctx, tx, queueID, projectID)
		if err != nil {
			return err
		}
		if locked.Version != input.ExpectedVersion || (locked.Status != string(library.ImportQueueUploading) && locked.Status != string(library.ImportQueueDraft)) {
			return library.ErrConflict
		}
		if len(items) > 0 {
			if err := saveImportQueueItemOrder(ctx, tx, projectID, queueID, items); err != nil {
				return err
			}
		}
		updated, err := sqlc.New(tx).SaveImportQueueVersion(ctx, sqlc.SaveImportQueueVersionParams{
			UpdatedAt: storedb.Timestamptz(time.Now().UTC().Truncate(time.Microsecond)), QueueID: queueID, ProjectID: projectID, ExpectedVersion: input.ExpectedVersion,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return library.ErrConflict
		}
		if err != nil {
			return err
		}
		queue = importQueueFromSQL(updated)
		queue.Items, err = loadImportQueueItems(ctx, tx, queueID, projectID)
		return err
	})
	if err != nil {
		if isUniqueViolation(err) {
			return library.ImportQueue{}, library.ErrConflict
		}
		return library.ImportQueue{}, mapQueueImportError(err)
	}
	return queue, nil
}

func (s *Store) CancelImportQueueForActor(ctx context.Context, input library.CancelImportQueueStoreInput) (library.ImportQueue, error) {
	projectID, actorID, queueID, err := parseQueueReadInput(input.ProjectID, input.ActorUserID, input.QueueID)
	if err != nil {
		return library.ImportQueue{}, err
	}
	if input.ExpectedVersion < 1 {
		return library.ImportQueue{}, library.ErrValidation
	}
	var queue library.ImportQueue
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockImportProjectActor(ctx, tx, projectID, actorID); err != nil {
			return err
		}
		canceledAt := time.Now().UTC().Truncate(time.Microsecond)
		queries := sqlc.New(tx)
		updated, err := queries.CancelImportQueueRow(ctx, sqlc.CancelImportQueueRowParams{
			UpdatedAt: storedb.Timestamptz(canceledAt), QueueID: queueID, ProjectID: projectID, ExpectedVersion: input.ExpectedVersion,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return library.ErrConflict
		}
		if err != nil {
			return err
		}
		if _, err := jobstore.FinalizeQueuedImportQueueSuffixInTx(ctx, tx, jobstore.FinalizeQueuedImportQueueSuffixInTxInput{
			ProjectID: projectID, QueueID: queueID, AfterPosition: 0,
			UpdatedAt: storedb.Timestamptz(canceledAt),
		}); errors.Is(err, jobstore.ErrImportTerminalConflict) {
			return library.ErrConflict
		} else if err != nil {
			return err
		}
		linkedJobs, err := queries.ListImportQueueLinkedJobsForCancel(ctx, sqlc.ListImportQueueLinkedJobsForCancelParams{
			QueueID: queueID, ProjectID: projectID,
		})
		if err != nil {
			return err
		}
		for _, linkedJob := range linkedJobs {
			var to jobs.Status
			switch jobs.Status(linkedJob.Status) {
			case jobs.StatusQueued:
				to = jobs.StatusCanceled
			case jobs.StatusRunning:
				to = jobs.StatusCanceling
			case jobs.StatusCanceling, jobs.StatusSucceeded, jobs.StatusFailed, jobs.StatusCanceled, jobs.StatusExpired:
				continue
			default:
				return library.ErrConflict
			}
			if _, err := s.jobs.CancelJobStatusInTx(ctx, tx, jobs.CancelJobStatusStoreInput{
				ActorUserID: actorID.String(), ProjectID: projectID.String(), JobID: linkedJob.ID.String(),
				From: jobs.Status(linkedJob.Status), To: to, UpdatedAt: canceledAt,
			}); err != nil {
				switch {
				case errors.Is(err, jobs.ErrForbidden):
					return library.ErrForbidden
				case errors.Is(err, jobs.ErrNotFound), errors.Is(err, jobs.ErrConflict):
					return library.ErrConflict
				default:
					return err
				}
			}
		}
		if _, err := queries.ScrubImportQueuePendingUploads(ctx, sqlc.ScrubImportQueuePendingUploadsParams{ProjectID: projectID, QueueID: queueID}); err != nil {
			return err
		}
		queue = importQueueFromSQL(updated)
		queue.Items, err = loadImportQueueItems(ctx, tx, queueID, projectID)
		return err
	})
	if err != nil {
		return library.ImportQueue{}, mapQueueImportError(err)
	}
	return queue, nil
}

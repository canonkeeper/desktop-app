package librarystore

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) StartImportQueueForActor(ctx context.Context, input library.StartImportQueueStoreInput) (library.ImportQueue, error) {
	projectID, actorID, queueID, err := parseQueueReadInput(input.ProjectID, input.ActorUserID, input.QueueID)
	if err != nil {
		return library.ImportQueue{}, err
	}
	if input.ExpectedVersion < 1 ||
		strings.TrimSpace(input.IdempotencyKey) == "" ||
		strings.TrimSpace(input.RequestMethod) == "" ||
		strings.TrimSpace(input.RequestPath) == "" ||
		strings.TrimSpace(input.RequestFingerprint) == "" {
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
		locked, err := lockImportQueue(ctx, tx, queueID, projectID)
		if err != nil {
			return err
		}
		queries := sqlc.New(tx)
		items, err := queries.ListImportQueueItemsForStart(ctx, sqlc.ListImportQueueItemsForStartParams{
			QueueID: queueID, ProjectID: projectID,
		})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return library.ErrValidation
		}
		if locked.Status == string(library.ImportQueueRunning) {
			if err := validateImportQueueStartReplay(locked, items, input); err != nil {
				return err
			}
			queue = importQueueFromSQL(locked)
			queue.Items, err = loadImportQueueItems(ctx, tx, queueID, projectID)
			return err
		}
		if locked.Version != input.ExpectedVersion || locked.Status != string(library.ImportQueueDraft) {
			return library.ErrConflict
		}
		nextBookOrder, err := validatedNextBookOrder(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if err := validateImportQueueStartItems(items, nextBookOrder); err != nil {
			return err
		}
		reserved, err := importQueueOrderIsReserved(ctx, tx, projectID, queueID, nextBookOrder, len(items))
		if err != nil {
			return err
		}
		if reserved {
			return library.ErrImportExpectedOrderConflict
		}
		startedAt := time.Now().UTC().Truncate(time.Microsecond)
		// Every volume of the queue is one new book of the project owner and one upload of the
		// actor; the queue moving to the next volume counts nothing more (R3.1, R3.2). The owner's
		// quota lock comes before the actor's day row, as on every path (owner-tools lock order).
		if err := quotastore.CheckNewBooksInTx(ctx, tx, projectID.String(), int64(len(items)), true); err != nil {
			return err
		}
		if err := quotastore.ReserveImportsInTx(ctx, tx, actorID.String(), int64(len(items)), startedAt); err != nil {
			return err
		}

		var projectGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation FROM projects WHERE id = $1 AND deleted_at IS NULL
		`, projectID).Scan(&projectGeneration); err != nil {
			return mapQueueNotFound(err)
		}
		for _, item := range items {
			extension := strings.ToLower(path.Ext(item.SourceFileName))
			initialPublication, err := library.InitialPublicationForImportExtension(extension)
			if err != nil {
				return err
			}
			progressJSON, err := jobstore.MarshalImportProgress(jobs.ImportProgress{
				Phase: jobs.ImportPhaseQueued, BytesTransferred: item.SizeBytes.Int64,
				BytesTotal: item.SizeBytes.Int64, QueuedAt: startedAt, TransferredAt: startedAt,
			}, jobs.StatusQueued)
			if err != nil {
				return library.ErrValidation
			}
			var jobID, jobOwnerID, jobRefID, requestOwnerID, requestRefID pgtype.UUID
			if err := tx.QueryRow(ctx, `
				SELECT gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid()
			`).Scan(&jobID, &jobOwnerID, &jobRefID, &requestOwnerID, &requestRefID); err != nil {
				return err
			}
			if err := s.createImportProjectArtifact(ctx, tx, projectID, projectGeneration,
				governance.ArtifactJob, jobID, jobOwnerID, jobRefID,
				func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
					_, err := queries.CreateImportQueueJobRecord(ctx, sqlc.CreateImportQueueJobRecordParams{
						ID: jobID, Kind: library.ImportJobKind, Status: library.ImportJobStatusQueued,
						ProgressJson: progressJSON, ExpectedBookOrder: item.ConfirmedBookOrder.Int32,
						ImportQueueID: queueID.String(), ImportQueueItemID: item.ID.String(),
						CreatorUserID: actorID, CanCancel: true, CreatedAt: storedb.Timestamptz(startedAt),
						UpdatedAt: storedb.Timestamptz(startedAt), ArtifactSourceOwnerID: ownerID, ProjectID: projectID,
						CreatorRequestID: storedb.CreatorRequestID(ctx),
					})
					return err
				},
			); err != nil {
				return err
			}
			if err := s.createImportProjectArtifact(ctx, tx, projectID, projectGeneration,
				governance.ArtifactImportJobRequest, jobID, requestOwnerID, requestRefID,
				func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
					return queries.CreateImportJobRequest(ctx, sqlc.CreateImportJobRequestParams{
						JobID: jobID, ProjectID: projectID, ActorUserID: actorID, StorageKey: item.StorageKey.String,
						IdempotencyKey: input.IdempotencyKey + ":" + item.ID.String(), RequestMethod: input.RequestMethod,
						RequestPath: input.RequestPath, RequestFingerprint: input.RequestFingerprint,
						SourceFileName: item.SourceFileName, Extension: extension, MimeType: item.MimeType.String,
						SizeBytes: item.SizeBytes.Int64, FileSha256: item.Sha256.String,
						ImportMode: string(library.ImportModeNewBook), InitialPublication: string(initialPublication),
						CanCancel: true, CreatedAt: storedb.Timestamptz(startedAt), UpdatedAt: storedb.Timestamptz(startedAt),
						ArtifactSourceOwnerID: ownerID,
					})
				},
			); err != nil {
				return err
			}
			if _, err := queries.MarkImportJobQueued(ctx, sqlc.MarkImportJobQueuedParams{
				EnqueuedAt: storedb.Timestamptz(startedAt), JobID: jobID, ProjectID: projectID, StorageKey: item.StorageKey.String,
			}); err != nil {
				return err
			}
			linked, err := queries.LinkImportQueueItemJob(ctx, sqlc.LinkImportQueueItemJobParams{
				JobID: jobID, UpdatedAt: storedb.Timestamptz(startedAt), ItemID: item.ID, QueueID: queueID, ProjectID: projectID,
			})
			if err != nil {
				return err
			}
			if linked != 1 {
				return library.ErrConflict
			}
		}
		updated, err := queries.StartImportQueueRow(ctx, sqlc.StartImportQueueRowParams{
			UpdatedAt: storedb.Timestamptz(startedAt), StartedAt: storedb.Timestamptz(startedAt),
			QueueID: queueID, ProjectID: projectID, ExpectedVersion: input.ExpectedVersion,
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

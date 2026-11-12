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
		if err := validateImportQueueStartItems(items); err != nil {
			return err
		}
		nextBookOrder, err := validatedNextBookOrder(ctx, tx, projectID)
		if err != nil {
			return err
		}
		reserved, err := importQueueOrderIsReserved(ctx, tx, projectID, queueID, nextBookOrder, len(items))
		if err != nil {
			return err
		}
		if reserved {
			return library.ErrImportExpectedOrderConflict
		}
		// The order of the volumes is the order of the rows (specs/2026-11-11-ux7-upload.md UP2.3): each volume takes the
		// place after the books of the shelf by its position, and keeps it while the queue runs.
		if err := assignImportQueueOrders(ctx, tx, projectID, queueID, nextBookOrder); err != nil {
			return err
		}
		for index := range items {
			items[index].ConfirmedBookOrder = pgtype.Int4{Int32: int32(nextBookOrder + index), Valid: true}
		}
		startedAt := time.Now().UTC().Truncate(time.Microsecond)
		// Every volume whose file passed the check is one new book of the project owner and one upload of the actor; the
		// queue moving to the next volume counts nothing more (R3.1, R3.2). A file that did not pass counts nothing until
		// «Заменить файл» gives it one that passes (UP3.2). The owner's quota lock comes before the actor's day row, as on
		// every path (owner-tools lock order).
		passed := 0
		for _, item := range items {
			if item.PreflightStatus == library.ImportQueueItemReady {
				passed++
			}
		}
		if passed > 0 {
			if err := s.quota.CheckNewBooksInTx(ctx, tx, projectID.String(), int64(passed), true); err != nil {
				return err
			}
			if err := s.quota.ReserveDailyInTx(ctx, tx, actorID.String(), quota.KeyImportsPerDay, int64(passed), startedAt); err != nil {
				return err
			}
		}

		var projectGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation FROM projects WHERE id = $1 AND deleted_at IS NULL
		`, projectID).Scan(&projectGeneration); err != nil {
			return mapQueueNotFound(err)
		}
		for _, item := range items {
			if item.PreflightStatus != library.ImportQueueItemReady {
				// The file goes nowhere: it is not kept for a Job that will not come.
				if _, err := queries.ScrubImportQueueItemUpload(ctx, sqlc.ScrubImportQueueItemUploadParams{
					ProjectID: projectID, StorageKey: item.StorageKey.String,
				}); err != nil {
					return err
				}
				continue
			}
			jobID, err := s.enqueueImportQueueVolumeInTx(ctx, tx, importQueueVolume{
				projectID: projectID, actorID: actorID, queueID: queueID, itemID: item.ID, generation: projectGeneration,
				sourceFileName: item.SourceFileName, storageKey: item.StorageKey.String, mimeType: item.MimeType.String,
				sha256: item.Sha256.String, sizeBytes: item.SizeBytes.Int64, order: item.ConfirmedBookOrder.Int32,
			}, importQueueVolumeRequest{
				idempotencyKey: input.IdempotencyKey + ":" + item.ID.String(), method: input.RequestMethod,
				path: input.RequestPath, fingerprint: input.RequestFingerprint,
			}, startedAt)
			if err != nil {
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

// importQueueVolume is a volume of a queue given its import Job: at the start of the queue, or again by «Заменить файл».
type importQueueVolume struct {
	projectID, actorID, queueID, itemID  pgtype.UUID
	generation                           int64
	sourceFileName, storageKey, mimeType string
	sha256                               string
	sizeBytes                            int64
	order                                int32
}

type importQueueVolumeRequest struct {
	idempotencyKey, method, path, fingerprint string
}

// enqueueImportQueueVolumeInTx creates the import Job of a volume at its place, with its request, and queues it; the
// caller links the Job to its volume.
func (s *Store) enqueueImportQueueVolumeInTx(
	ctx context.Context, tx pgx.Tx, volume importQueueVolume, request importQueueVolumeRequest, at time.Time,
) (pgtype.UUID, error) {
	queries := sqlc.New(tx)
	extension := library.ImportFileKind(volume.sourceFileName)
	initialPublication, err := library.InitialPublicationForImportExtension(extension)
	if err != nil {
		return pgtype.UUID{}, err
	}
	progressJSON, err := jobstore.MarshalImportProgress(jobs.ImportProgress{
		Phase: jobs.ImportPhaseQueued, BytesTransferred: volume.sizeBytes,
		BytesTotal: volume.sizeBytes, QueuedAt: at, TransferredAt: at,
	}, jobs.StatusQueued)
	if err != nil {
		return pgtype.UUID{}, library.ErrValidation
	}
	var jobID, jobOwnerID, jobRefID, requestOwnerID, requestRefID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid()
	`).Scan(&jobID, &jobOwnerID, &jobRefID, &requestOwnerID, &requestRefID); err != nil {
		return pgtype.UUID{}, err
	}
	if err := s.createImportProjectArtifact(ctx, tx, volume.projectID, volume.generation,
		governance.ArtifactJob, jobID, jobOwnerID, jobRefID,
		func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
			_, err := queries.CreateImportQueueJobRecord(ctx, sqlc.CreateImportQueueJobRecordParams{
				ID: jobID, Kind: library.ImportJobKind, Status: library.ImportJobStatusQueued,
				ProgressJson: progressJSON, ExpectedBookOrder: volume.order,
				ImportQueueID: volume.queueID.String(), ImportQueueItemID: volume.itemID.String(),
				CreatorUserID: volume.actorID, CanCancel: true, CreatedAt: storedb.Timestamptz(at),
				UpdatedAt: storedb.Timestamptz(at), ArtifactSourceOwnerID: ownerID, ProjectID: volume.projectID,
				CreatorRequestID: storedb.CreatorRequestID(ctx),
			})
			return err
		},
	); err != nil {
		return pgtype.UUID{}, err
	}
	if err := s.createImportProjectArtifact(ctx, tx, volume.projectID, volume.generation,
		governance.ArtifactImportJobRequest, jobID, requestOwnerID, requestRefID,
		func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
			return queries.CreateImportJobRequest(ctx, sqlc.CreateImportJobRequestParams{
				JobID: jobID, ProjectID: volume.projectID, ActorUserID: volume.actorID, StorageKey: volume.storageKey,
				IdempotencyKey: request.idempotencyKey, RequestMethod: request.method,
				RequestPath: request.path, RequestFingerprint: request.fingerprint,
				SourceFileName: volume.sourceFileName, Extension: extension, MimeType: volume.mimeType,
				SizeBytes: volume.sizeBytes, FileSha256: volume.sha256,
				ImportMode: string(library.ImportModeNewBook), InitialPublication: string(initialPublication),
				CanCancel: true, CreatedAt: storedb.Timestamptz(at), UpdatedAt: storedb.Timestamptz(at),
				ArtifactSourceOwnerID: ownerID,
			})
		},
	); err != nil {
		return pgtype.UUID{}, err
	}
	if _, err := queries.MarkImportJobQueued(ctx, sqlc.MarkImportJobQueuedParams{
		EnqueuedAt: storedb.Timestamptz(at), JobID: jobID, ProjectID: volume.projectID, StorageKey: volume.storageKey,
	}); err != nil {
		return pgtype.UUID{}, err
	}
	return jobID, nil
}

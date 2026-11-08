package deletion

import (
	"context"
	"errors"
	"time"

	deletion "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// filesystemSelection names the book covers of one source hard purge: every cover of a project,
// or the covers of one book.
type filesystemSelection struct {
	projectID   pgtype.UUID
	tombstoneID pgtype.UUID
	kind        governance.DeletionSelectionKind
	bookID      pgtype.UUID
	mode        governance.PurgeMode
}

func (store *Store) BeginFilesystemDeletion(
	ctx context.Context,
	request deletion.TargetRequest,
) ([]deletionservice.FilesystemDeletionObject, error) {
	selection, err := filesystemSelectionForRequest(request)
	if err != nil || store == nil || store.DB == nil || store.queries == nil {
		return nil, deletion.ErrInvalidTargetRequest
	}
	var objects []deletionservice.FilesystemDeletionObject
	err = store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockFilesystemTargetRequest(ctx, store.queries.WithTx(tx), request, selection.projectID, selection.tombstoneID); err != nil {
			return err
		}
		var err error
		objects, err = bookCoverDeletionObjects(ctx, tx, selection)
		return err
	})
	if err != nil {
		return nil, err
	}
	return objects, nil
}

func (store *Store) CommitFilesystemObjectDeletion(
	ctx context.Context,
	request deletion.TargetRequest,
	object deletionservice.FilesystemDeletionObject,
	receipt governance.Digest,
) error {
	selection, err := filesystemSelectionForRequest(request)
	if err != nil ||
		(object.State != deletionservice.FilesystemObjectDeleting && object.State != deletionservice.FilesystemObjectDeleted) ||
		receipt == (governance.Digest{}) ||
		store == nil || store.DB == nil || store.queries == nil {
		return deletion.ErrInvalidTargetRequest
	}
	wantReceipt, err := deletionservice.FilesystemObjectReceiptDigest(
		request.Claim.ProjectID,
		object.ArtifactID,
		object.DeleteOperationID,
	)
	if err != nil || receipt != wantReceipt {
		return deletionservice.ErrFilesystemTargetConflict
	}
	return store.commitBookCoverDeletion(ctx, request, selection, object)
}

func (store *Store) VerifyFilesystemDeletionComplete(
	ctx context.Context,
	request deletion.TargetRequest,
) error {
	selection, err := filesystemSelectionForRequest(request)
	if err != nil || store == nil || store.DB == nil || store.queries == nil {
		return deletion.ErrInvalidTargetRequest
	}
	return store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockFilesystemTargetRequest(ctx, store.queries.WithTx(tx), request, selection.projectID, selection.tombstoneID); err != nil {
			return err
		}
		covers, err := bookCoverDeletionObjects(ctx, tx, selection)
		if err != nil {
			return err
		}
		if len(covers) != 0 {
			return deletionservice.ErrFilesystemTargetConflict
		}
		return nil
	})
}

func lockFilesystemTargetRequest(
	ctx context.Context,
	queries *sqlc.Queries,
	request deletion.TargetRequest,
	projectID pgtype.UUID,
	tombstoneID pgtype.UUID,
) error {
	task, err := queries.GetDeletionTargetTaskForCompletion(
		ctx,
		deletionTaskForCompletionParams(request.Claim, projectID, tombstoneID),
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return deletion.ErrLostTargetClaim
	}
	if err != nil {
		return err
	}
	if task.State != string(deletion.TargetProcessing) {
		return deletion.ErrLostTargetClaim
	}
	tombstoneRow, err := queries.GetDeletionTombstoneForTarget(ctx, sqlc.GetDeletionTombstoneForTargetParams{
		TombstoneID: tombstoneID,
		ProjectID:   projectID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return deletion.ErrLostTargetClaim
	}
	if err != nil {
		return err
	}
	tombstone, err := deletionTombstoneFromFields(deletionTombstoneFieldsFromTarget(tombstoneRow))
	if err != nil {
		return err
	}
	persisted, err := deletionRequestForPersistedTask(request.Claim, tombstone, task)
	if err != nil || persisted != request || tombstone.Status != deletion.TombstoneProcessing {
		return deletion.ErrLostTargetClaim
	}
	return nil
}

func filesystemSelectionForRequest(request deletion.TargetRequest) (filesystemSelection, error) {
	if request.Claim.Target != governance.TargetFilesystem || request.Validate(time.Now().UTC()) != nil {
		return filesystemSelection{}, deletion.ErrInvalidTargetRequest
	}
	projectID, tombstoneID, err := deletionClaimUUIDs(request.Claim)
	if err != nil {
		return filesystemSelection{}, err
	}
	fields, err := request.Tombstone.Identity.SelectedVector.Fields()
	if err != nil {
		return filesystemSelection{}, deletion.ErrInvalidTargetRequest
	}
	selection := filesystemSelection{
		projectID: projectID, tombstoneID: tombstoneID, kind: fields.Kind, mode: request.Tombstone.Identity.Mode,
	}
	if fields.Kind == governance.DeletionSelectionBook {
		selection.bookID, err = storedb.ParseUUID(fields.Source.BookID)
		if err != nil {
			return filesystemSelection{}, deletion.ErrInvalidTargetRequest
		}
	}
	return selection, nil
}

func bookCoverDeletionObjects(ctx context.Context, tx pgx.Tx, selection filesystemSelection) ([]deletionservice.FilesystemDeletionObject, error) {
	if selection.mode != governance.PurgeModeSourceHardPurge {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT cover.id, cover.storage_key, cover.object_deleted_at
		FROM book_cover_artifacts AS cover
		WHERE cover.project_id = $1
		  AND (
		    $2 = 'project'
		    OR ($2 = 'book' AND cover.book_id = $3)
		  )
		ORDER BY cover.id
		FOR UPDATE OF cover
	`, selection.projectID, string(selection.kind), selection.bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objects := make([]deletionservice.FilesystemDeletionObject, 0)
	for rows.Next() {
		var id pgtype.UUID
		var key string
		var objectDeletedAt pgtype.Timestamptz
		if err := rows.Scan(&id, &key, &objectDeletedAt); err != nil {
			return nil, err
		}
		object := deletionservice.FilesystemDeletionObject{
			ArtifactID: id.String(), DeleteOperationID: selection.tombstoneID.String(),
			ObjectKey: key, State: deletionservice.FilesystemObjectDeleting,
		}
		if objectDeletedAt.Valid {
			receipt, err := deletionservice.FilesystemObjectReceiptDigest(
				selection.projectID.String(), object.ArtifactID, object.DeleteOperationID,
			)
			if err != nil {
				return nil, err
			}
			object.State = deletionservice.FilesystemObjectDeleted
			object.DeletionReceiptDigest = receipt
		}
		objects = append(objects, object)
	}
	return objects, rows.Err()
}

func (store *Store) commitBookCoverDeletion(
	ctx context.Context,
	request deletion.TargetRequest,
	selection filesystemSelection,
	object deletionservice.FilesystemDeletionObject,
) error {
	artifactID, artifactErr := storedb.ParseUUID(object.ArtifactID)
	operationID, operationErr := storedb.ParseUUID(object.DeleteOperationID)
	if selection.mode != governance.PurgeModeSourceHardPurge || artifactErr != nil || operationErr != nil ||
		operationID != selection.tombstoneID {
		return deletionservice.ErrFilesystemTargetConflict
	}
	return store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockFilesystemTargetRequest(ctx, store.queries.WithTx(tx), request, selection.projectID, selection.tombstoneID); err != nil {
			return err
		}
		var deletedID pgtype.UUID
		err := tx.QueryRow(ctx, `
			DELETE FROM book_cover_artifacts AS cover
			WHERE cover.id = $1 AND cover.project_id = $2 AND cover.storage_key = $3
			  AND (
			    $4 = 'project'
			    OR ($4 = 'book' AND cover.book_id = $5)
			  )
			RETURNING cover.id
		`, artifactID, selection.projectID, object.ObjectKey,
			string(selection.kind), selection.bookID).Scan(&deletedID)
		if errors.Is(err, pgx.ErrNoRows) || deletedID != artifactID {
			return deletionservice.ErrFilesystemTargetConflict
		}
		return err
	})
}

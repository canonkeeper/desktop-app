package librarystore

import (
	"context"
	"errors"
	"strings"

	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type importQueueRow interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func lockImportQueue(ctx context.Context, tx importQueueRow, queueID, projectID pgtype.UUID) (sqlc.ImportQueue, error) {
	var row sqlc.ImportQueue
	err := tx.QueryRow(ctx, `
		SELECT id, project_id, created_by_user_id, idempotency_key, request_fingerprint,
		       status, version, created_at, updated_at, started_at, completed_at
		FROM import_queues
		WHERE id = $1 AND project_id = $2
		FOR UPDATE
	`, queueID, projectID).Scan(
		&row.ID, &row.ProjectID, &row.CreatedByUserID, &row.IdempotencyKey, &row.RequestFingerprint,
		&row.Status, &row.Version, &row.CreatedAt, &row.UpdatedAt, &row.StartedAt, &row.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.ImportQueue{}, library.ErrNotFound
	}
	return row, err
}

func loadImportQueueItems(ctx context.Context, queryer sqlc.DBTX, queueID, projectID pgtype.UUID) ([]library.ImportQueueItem, error) {
	rows, err := sqlc.New(queryer).ListImportQueueItems(ctx, sqlc.ListImportQueueItemsParams{QueueID: queueID, ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	items := make([]library.ImportQueueItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, importQueueItemFromSQL(row))
	}
	return items, nil
}

// validateImportQueueStartItems checks the volumes of a queue to start: every file received, a file that passed the
// check with its upload intact. A file that did not pass starts nothing and waits for the author at its place
// (specs/2026-11-11-ux7-upload.md UP1.2, UP3.2).
func validateImportQueueStartItems(items []sqlc.ListImportQueueItemsForStartRow) error {
	if len(items) == 0 {
		return library.ErrValidation
	}
	for index, item := range items {
		if item.Position != int32(index+1) {
			return library.ErrValidation
		}
		if item.PreflightStatus == library.ImportQueueItemInvalid {
			continue
		}
		if item.PreflightStatus != library.ImportQueueItemReady ||
			!item.StorageKey.Valid || !item.MimeType.Valid || !item.Sha256.Valid ||
			!item.SizeBytes.Valid || item.SizeBytes.Int64 <= 0 {
			return library.ErrValidation
		}
		if !item.UploadPresent || !item.UploadOwnerActive || !item.UploadSizeMatches {
			return library.ErrValidation
		}
		if !item.UploadChecksumMatches {
			return library.ErrImportContentMismatch
		}
	}
	return nil
}

// assignImportQueueOrders gives every volume of the queue its place by its position, from the first place after the
// books of the shelf.
func assignImportQueueOrders(ctx context.Context, tx pgx.Tx, projectID, queueID pgtype.UUID, firstOrder int) error {
	_, err := tx.Exec(ctx, `
		UPDATE import_queue_items
		SET confirmed_book_order = $3::integer + position - 1, updated_at = clock_timestamp()
		WHERE queue_id = $1 AND project_id = $2
	`, queueID, projectID, firstOrder)
	return err
}

func validateImportQueueStartReplay(
	locked sqlc.ImportQueue,
	items []sqlc.ListImportQueueItemsForStartRow,
	input library.StartImportQueueStoreInput,
) error {
	if locked.Status != string(library.ImportQueueRunning) || input.ExpectedVersion != locked.Version-1 {
		return library.ErrConflict
	}
	if err := validateImportQueueStartItems(items); err != nil {
		return err
	}
	firstOrder := items[0].ConfirmedBookOrder.Int32
	for index, item := range items {
		if !item.ConfirmedBookOrder.Valid || item.ConfirmedBookOrder.Int32 != firstOrder+int32(index) {
			return library.ErrConflict
		}
		if item.PreflightStatus == library.ImportQueueItemInvalid && !item.ImportJobID.Valid {
			continue
		}
		if !item.ImportJobID.Valid ||
			optionalText(item.RequestIdempotencyKey) != input.IdempotencyKey+":"+item.ID.String() ||
			optionalText(item.RequestMethod) != input.RequestMethod ||
			optionalText(item.RequestPath) != input.RequestPath ||
			optionalText(item.RequestFingerprint) != input.RequestFingerprint ||
			item.SubjectQueueID != locked.ID.String() ||
			item.SubjectItemID != item.ID.String() ||
			item.SubjectBookOrder != item.ConfirmedBookOrder.Int32 {
			return library.ErrConflict
		}
	}
	return nil
}

func importQueueOrderIsReserved(ctx context.Context, tx pgx.Tx, projectID, queueID pgtype.UUID, firstOrder, itemCount int) (bool, error) {
	orders := make([]int32, itemCount)
	for index := range orders {
		orders[index] = int32(firstOrder + index)
	}
	var activeQueue, bookConflict, importJobConflict bool
	err := tx.QueryRow(ctx, `
		SELECT
		  EXISTS (SELECT 1 FROM import_queues WHERE project_id = $1 AND id <> $2 AND status IN ('uploading','draft','running')),
		  EXISTS (SELECT 1 FROM books WHERE project_id = $1 AND deleted_at IS NULL AND "order" = ANY($3::integer[])),
		  EXISTS (
		    SELECT 1 FROM jobs
		    WHERE project_id = $1 AND kind = 'import'
		      AND status IN ('queued','running')
		      AND (subject_json->>'expectedBookOrder')::integer = ANY($3::integer[])
		  )
	`, projectID, queueID, orders).Scan(&activeQueue, &bookConflict, &importJobConflict)
	return activeQueue || bookConflict || importJobConflict, err
}

func importQueueFromSQL(row sqlc.ImportQueue) library.ImportQueue {
	return library.ImportQueue{
		ID: storedb.UUIDString(row.ID), ProjectID: storedb.UUIDString(row.ProjectID), CreatedByUserID: storedb.UUIDString(row.CreatedByUserID),
		Status: library.ImportQueueStatus(row.Status), Version: row.Version,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		StartedAt: storedb.OptionalTime(row.StartedAt), CompletedAt: storedb.OptionalTime(row.CompletedAt),
	}
}

func importQueueItemFromSQL(row sqlc.ListImportQueueItemsRow) library.ImportQueueItem {
	return library.ImportQueueItem{
		ID: storedb.UUIDString(row.ID), QueueID: storedb.UUIDString(row.QueueID), ProjectID: storedb.UUIDString(row.ProjectID),
		ClientItemID: row.ClientItemID, Position: int(row.Position), SourceFileName: row.SourceFileName,
		StorageKey: optionalText(row.StorageKey), MIMEType: optionalText(row.MimeType), SizeBytes: optionalInt8(row.SizeBytes),
		SHA256: optionalText(row.Sha256), ConfirmedBookOrder: optionalInt4Value(row.ConfirmedBookOrder),
		PreflightStatus: row.PreflightStatus, PreflightErrorJSON: append([]byte(nil), row.PreflightErrorJson...),
		ImportJobID: storedb.UUIDString(row.ImportJobID), ImportJobStatus: row.ImportJobStatus, ImportJobRetryCount: int(row.ImportJobRetryCount),
		ImportJobErrorJSON: append([]byte(nil), row.ImportJobErrorJson...), BookID: storedb.UUIDString(row.BookID),
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time, SkippedAt: storedb.OptionalTime(row.SkippedAt),
	}
}

func parseQueueProjectActor(projectID, actorID string) (pgtype.UUID, pgtype.UUID, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, library.ErrNotFound
	}
	actor, err := storedb.ParseUUID(actorID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, library.ErrNotFound
	}
	return project, actor, nil
}

func parseQueueReadInput(projectID, actorID, queueID string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	project, actor, err := parseQueueProjectActor(projectID, actorID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	queue, err := storedb.ParseUUID(queueID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, library.ErrNotFound
	}
	return project, actor, queue, nil
}

func parseQueueItemInput(projectID, actorID, queueID, itemID string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	project, actor, queue, err := parseQueueReadInput(projectID, actorID, queueID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	item, err := storedb.ParseUUID(itemID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, library.ErrNotFound
	}
	return project, actor, queue, item, nil
}

func optionalQueueUUID(value string) (pgtype.UUID, error) {
	if strings.TrimSpace(value) == "" {
		return pgtype.UUID{}, nil
	}
	return storedb.ParseUUID(value)
}

func optionalInt4Value(value pgtype.Int4) int {
	if !value.Valid {
		return 0
	}
	return int(value.Int32)
}

func optionalInt8(value pgtype.Int8) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func dbText(value string) pgtype.Text { return pgtype.Text{String: value, Valid: true} }
func dbInt8(value int64) pgtype.Int8  { return pgtype.Int8{Int64: value, Valid: true} }
func optionalText(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func mapQueueNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return library.ErrNotFound
	}
	return err
}

func mapQueueImportError(err error) error {
	if errors.Is(err, library.ErrNotFound) || errors.Is(err, library.ErrConflict) || errors.Is(err, library.ErrForbidden) {
		return err
	}
	return mapQueueNotFound(err)
}

func isQueueActiveUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "import_queues_one_active_per_project"
}

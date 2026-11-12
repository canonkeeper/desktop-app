package authormaterials

import (
	"context"
	"encoding/json"
	"errors"

	domain "canonkeeper/backend/internal/domain/authormaterials"
	service "canonkeeper/backend/internal/services/authormaterials"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// The records the author writes on the page (specs/2026-11-10-ux8-memory-records.md MR2.1, MR2.3): one by hand, or a change
// of one. One name is one record: a name another record has is domain.NameTaken with that record, so the page offers to
// open it.

func isNameTaken(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

// nameTaken is the refusal of a name: the record of the project that has it.
func nameTaken(ctx context.Context, queries *sqlc.Queries, project pgtype.UUID, key string) error {
	id, err := queries.AuthorMaterialOfKey(ctx, sqlc.AuthorMaterialOfKeyParams{ProjectID: project, NameKey: key})
	if err != nil {
		return err
	}
	return domain.NameTaken{RecordID: storedb.UUIDString(id)}
}

type createReplay struct {
	MaterialID string `json:"materialId"`
}

// CreateAuthorMaterial keeps a record of the source «вручную» under its idempotency key. A new record changes the revision
// of the materials the map is built on, so the map rows fall behind as they do after a change.
func (repository *Repository) CreateAuthorMaterial(ctx context.Context, command service.CreateCommand) (domain.Record, error) {
	properties, err := propertiesJSON(command.Draft.Properties)
	if err != nil {
		return domain.Record{}, domain.ErrInvalid
	}
	var record domain.Record
	err = repository.writeTx(ctx, command.Write, func(_ pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		idempotency, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ScopeHash: command.ScopeHash, OperationID: command.OperationID, IdempotencyKeyHash: command.KeyHash,
			RequestHash: command.RequestHash, StatusCode: 201, ResponseJson: []byte("{}"), CreatedAt: storedb.Timestamptz(command.At),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			kept, err := queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
				ScopeHash: command.ScopeHash, OperationID: command.OperationID, IdempotencyKeyHash: command.KeyHash,
			})
			if err != nil {
				return err
			}
			var replay createReplay
			if kept.RequestHash != command.RequestHash || json.Unmarshal(kept.ResponseJson, &replay) != nil {
				return domain.ErrConflict
			}
			id, err := storedb.ParseUUID(replay.MaterialID)
			if err != nil {
				return domain.ErrConflict
			}
			// The record may have been removed since: the first answer cannot be given again.
			record, err = getInTx(ctx, queries, project, id)
			return err
		}
		if err != nil {
			return err
		}
		id, err := queries.CreateAuthorMaterial(ctx, sqlc.CreateAuthorMaterialParams{
			ProjectID: project, Name: command.Draft.Name, NameKey: command.Key, Kind: string(command.Draft.Kind),
			Description: command.Draft.Description, Properties: properties, Section: command.Draft.Section, Actor: actor,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nameTaken(ctx, queries, project, command.Key)
		}
		if err != nil {
			return err
		}
		if record, err = getInTx(ctx, queries, project, id); err != nil {
			return err
		}
		response, err := json.Marshal(createReplay{MaterialID: record.ID})
		if err != nil {
			return err
		}
		_, err = queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{ID: idempotency.ID, StatusCode: 201, ResponseJson: response})
		return err
	})
	return record, err
}

func getInTx(ctx context.Context, queries *sqlc.Queries, project, id pgtype.UUID) (domain.Record, error) {
	row, err := queries.GetAuthorMaterial(ctx, sqlc.GetAuthorMaterialParams{ProjectID: project, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Record{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Record{}, err
	}
	return recordOf(row)
}

// UpdateAuthorMaterial replaces what a record says at the expected revision. The change runs under a savepoint, so a name
// another record has leaves the transaction able to name that record.
func (repository *Repository) UpdateAuthorMaterial(ctx context.Context, write service.Write, recordID string, expected int64, draft domain.Draft, key string) (domain.Record, error) {
	properties, err := propertiesJSON(draft.Properties)
	if err != nil {
		return domain.Record{}, domain.ErrInvalid
	}
	var record domain.Record
	err = repository.writeTx(ctx, write, func(tx pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		id, err := lockAtRevision(ctx, queries, project, recordID, expected)
		if err != nil {
			return err
		}
		savepoint, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := sqlc.New(savepoint).UpdateAuthorMaterial(ctx, sqlc.UpdateAuthorMaterialParams{
			ProjectID: project, ID: id, Name: draft.Name, NameKey: key, Kind: string(draft.Kind), Description: draft.Description,
			Properties: properties, Section: draft.Section, Actor: actor, ExpectedRevision: expected,
		}); err != nil {
			if rollback := savepoint.Rollback(ctx); rollback != nil {
				return rollback
			}
			if isNameTaken(err) {
				return nameTaken(ctx, queries, project, key)
			}
			return err
		}
		if err := savepoint.Commit(ctx); err != nil {
			return err
		}
		record, err = getInTx(ctx, queries, project, id)
		return err
	})
	return record, err
}

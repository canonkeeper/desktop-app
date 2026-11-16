// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"canonkeeper/backend/internal/auth"
	service "canonkeeper/backend/internal/services/obsidian"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// exportOperation names an export in idempotency_records (specs/2026-11-15-desk-obsidian-export.md OE1.1).
const exportOperation = "obsidian.export.v1"

var _ service.Exports = (*Exports)(nil)

// Exports keep the folder of the export of a saga on its link (obsidian_vaults.export_dir, last_export_at) and the answers of
// the exports that ended by their keys.
type Exports struct {
	db *storedb.DB
}

func NewExports(db *storedb.DB) *Exports {
	if db == nil {
		panic("obsidian exports: the database is required")
	}
	return &Exports{db: db}
}

// BeginExport is the one short transaction an export starts with (OE1.1): the saga alive and held, the proof of the session,
// the role of an editor or above and the link of the vault, by fenceInTx; then its title, the path of its vault and the folder
// of its export.
func (exports *Exports) BeginExport(ctx context.Context, actorUserID, projectID string, proof auth.SessionWriteProof) (service.ExportStart, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ExportStart{}, service.ErrNotFound
	}
	actor, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return service.ExportStart{}, service.ErrNotFound
	}
	var start service.ExportStart
	err = exports.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := fenceInTx(ctx, tx, project, actor, proof); err != nil {
			return err
		}
		var dir pgtype.Text
		err := tx.QueryRow(ctx, `
			SELECT p.name, v.vault_path, v.export_dir
			FROM projects AS p JOIN obsidian_vaults AS v ON v.project_id = p.id
			WHERE p.id = $1
		`, project).Scan(&start.Title, &start.VaultPath, &dir)
		start.ExportDir = dir.String
		return err
	})
	return start, err
}

// KeepExportDir writes the folder a first export chose while the link is the one the export started with (OE1.5).
func (exports *Exports) KeepExportDir(ctx context.Context, projectID, vaultPath, dir string) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrNotFound
	}
	tag, err := exports.db.Pool.Exec(ctx, `
		UPDATE obsidian_vaults SET export_dir = $3 WHERE project_id = $1 AND vault_path = $2
	`, project, vaultPath, dir)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return service.ErrNotLinked
	}
	return nil
}

// Linked says the link of the saga is the one the export started with (OE1.2): the path of its vault and the folder of this
// export — a link taken off and given again, to the same path too, is a new row without a folder.
func (exports *Exports) Linked(ctx context.Context, projectID, vaultPath, dir string) (bool, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return false, service.ErrNotFound
	}
	var linked bool
	err = exports.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM obsidian_vaults WHERE project_id = $1 AND vault_path = $2 AND export_dir = $3)
	`, project, vaultPath, dir).Scan(&linked)
	return linked, err
}

// EndExport writes the time of an export that ended while the link is the one it started with (OE1.2, OE1.8).
func (exports *Exports) EndExport(ctx context.Context, projectID, vaultPath, dir string, at time.Time) (bool, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return false, service.ErrNotFound
	}
	tag, err := exports.db.Pool.Exec(ctx, `
		UPDATE obsidian_vaults SET last_export_at = $4 WHERE project_id = $1 AND vault_path = $2 AND export_dir = $3
	`, project, vaultPath, dir, storedb.Timestamptz(at))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ExportReplay is the answer kept for the key of an export: nil for none, service.ErrReplayConflict for the key of another
// request.
func (exports *Exports) ExportReplay(ctx context.Context, key service.ReplayKey) (*service.Exported, error) {
	record, err := sqlc.New(exports.db.Pool).GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
		ScopeHash: scopeOf(key), OperationID: exportOperation, IdempotencyKeyHash: hashOf(key.Key),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var exported service.Exported
	if record.RequestHash != key.Request || json.Unmarshal(record.ResponseJson, &exported) != nil {
		return nil, service.ErrReplayConflict
	}
	return &exported, nil
}

// KeepExport keeps the answer of an export that ended for its key; a key kept already keeps its first answer.
func (exports *Exports) KeepExport(ctx context.Context, key service.ReplayKey, exported service.Exported) error {
	response, err := json.Marshal(exported)
	if err != nil {
		return err
	}
	_, err = sqlc.New(exports.db.Pool).CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
		ScopeHash: scopeOf(key), OperationID: exportOperation, IdempotencyKeyHash: hashOf(key.Key), RequestHash: key.Request,
		StatusCode: 200, ResponseJson: response, CreatedAt: storedb.Timestamptz(time.Now()),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

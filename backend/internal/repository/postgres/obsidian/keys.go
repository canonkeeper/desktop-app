// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"errors"

	service "canonkeeper/backend/internal/services/obsidian"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// QueueOfKey says whether the author made a queue of the volumes of the saga under the key — the key of the first step of
// a load (specs/2026-11-14-desk-obsidian-books.md OK1.6): a load whose key made its queue and kept no answer was
// interrupted.
func (books *Books) QueueOfKey(ctx context.Context, actorUserID, projectID, key string) (bool, error) {
	actor, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return false, service.ErrNotFound
	}
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return false, service.ErrNotFound
	}
	var made bool
	err = books.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM import_queues WHERE project_id = $1 AND created_by_user_id = $2 AND idempotency_key = $3)
	`, project, actor, key).Scan(&made)
	return made, err
}

// JobOfKey is the import Job the author started under the key of a request — the key of an update the bridge applies
// (OK2.5) — by the record of the request the core keeps for its replay.
func (books *Books) JobOfKey(ctx context.Context, actorUserID, method, path, key string) (string, bool, error) {
	actor, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return "", false, service.ErrNotFound
	}
	row, err := sqlc.New(books.db.Pool).GetImportJobByIdempotencyKey(ctx, sqlc.GetImportJobByIdempotencyKeyParams{
		ActorUserID: actor, RequestMethod: method, RequestPath: path, IdempotencyKey: key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return storedb.UUIDString(row.ID), true, nil
}

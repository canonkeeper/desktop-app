// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	service "canonkeeper/backend/internal/services/obsidian"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// loadOperation names a load of the books of the folders in idempotency_records
// (specs/2026-11-14-desk-obsidian-books.md OK1.6).
const loadOperation = "obsidian.books.v1"

var _ service.Replays = (*Replays)(nil)

// Replays keep the answers of the loads by their keys in the replay records of the core.
type Replays struct {
	db *storedb.DB
}

func NewReplays(db *storedb.DB) *Replays {
	if db == nil {
		panic("obsidian replays: the database is required")
	}
	return &Replays{db: db}
}

func hashOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// scopeOf is the scope of a key: the author in the saga.
func scopeOf(key service.ReplayKey) string {
	return hashOf("user:" + key.ActorUserID + "\x00project:" + key.ProjectID)
}

// Replay is the answer kept for the key: nil for none, service.ErrReplayConflict for the key of another request.
func (replays *Replays) Replay(ctx context.Context, key service.ReplayKey) (*service.Loaded, error) {
	record, err := sqlc.New(replays.db.Pool).GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
		ScopeHash: scopeOf(key), OperationID: loadOperation, IdempotencyKeyHash: hashOf(key.Key),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var loaded service.Loaded
	if record.RequestHash != key.Request || json.Unmarshal(record.ResponseJson, &loaded) != nil {
		return nil, service.ErrReplayConflict
	}
	return &loaded, nil
}

// Keep keeps the answer of the key; a key kept already keeps its first answer.
func (replays *Replays) Keep(ctx context.Context, key service.ReplayKey, loaded service.Loaded) error {
	response, err := json.Marshal(loaded)
	if err != nil {
		return err
	}
	_, err = sqlc.New(replays.db.Pool).CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
		ScopeHash: scopeOf(key), OperationID: loadOperation, IdempotencyKeyHash: hashOf(key.Key), RequestHash: key.Request,
		StatusCode: 200, ResponseJson: response, CreatedAt: storedb.Timestamptz(time.Now()),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

package storedb

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type migrationQuiescenceQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// MigrationQuiescent reports whether a stopped TEST contour has no executing
// work that an artifact cutover could strand. Durable queued Jobs resume after
// the replacement runtime starts. A nonterminal Job blocks the cutover while it
// is still claimable: the count is fail-closed over three arms — a null owner,
// a missing or mismatched sealed job owner, and a readable sealed owner all
// count as executing work. The only ignored shape is a nonterminal Job whose
// exact sealed job artifact owner exists but is no longer readable, because
// governed source deletion fenced the claim incarnation away. Unknown future
// statuses stay outside the terminal list and therefore keep blocking.
func MigrationQuiescent(ctx context.Context, db *DB) (bool, error) {
	if db == nil || db.Pool == nil {
		return false, errors.New("migration database is nil")
	}
	return checkMigrationQuiescence(ctx, db.Pool)
}

func checkMigrationQuiescence(ctx context.Context, db migrationQuiescenceQueryer) (bool, error) {
	// Read-only single count SELECT; unqualified names resolve through the
	// session search_path, which also keeps isolated schema fixtures honest.
	const query = `
SELECT count(*)::bigint
FROM jobs AS job
WHERE job.status NOT IN ('queued', 'succeeded', 'failed', 'canceled', 'expired')
  AND (
    job.artifact_source_owner_id IS NULL
    OR NOT EXISTS (
      SELECT 1
      FROM artifact_source_owners AS sealed
      WHERE sealed.id = job.artifact_source_owner_id
        AND sealed.project_id = job.project_id
        AND sealed.artifact_kind = 'job'
        AND sealed.artifact_id = job.id
    )
    OR ck_artifact_source_owner_is_readable(job.artifact_source_owner_id, job.project_id, 'job', job.id)
  )`
	var jobs int64
	if err := db.QueryRow(ctx, query).Scan(&jobs); err != nil {
		return false, fmt.Errorf("check migration quiescence: %w", err)
	}
	return jobs == 0, nil
}

package governancestore

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func LockImportArtifactOwnerSet(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	ownerIDs ...pgtype.UUID,
) error {
	if tx == nil || len(ownerIDs) != 3 {
		return ErrArtifactSourcesValidation
	}
	for _, ownerID := range ownerIDs {
		if !ownerID.Valid {
			return ErrArtifactSourcesConflict
		}
	}

	ids := make([]string, 0, len(ownerIDs))
	for _, ownerID := range ownerIDs {
		ids = append(ids, ownerID.String())
	}
	sort.Strings(ids)
	rows, err := tx.Query(ctx, `
		SELECT id
		FROM artifact_source_owners
		WHERE project_id = $1 AND id = ANY($2::uuid[])
		ORDER BY id
		FOR UPDATE
	`, projectID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	lockedCount := 0
	for rows.Next() {
		var ignored pgtype.UUID
		if err := rows.Scan(&ignored); err != nil {
			return err
		}
		lockedCount++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if lockedCount != len(ownerIDs) {
		return ErrArtifactSourcesConflict
	}
	return nil
}

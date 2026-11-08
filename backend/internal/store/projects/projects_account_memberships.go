package projectstore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// RemoveAccountMemberships takes a deleted account out of every project it does not own (UC3,
// R3.4). In a live project it goes the way "remove member" does: removed_at under the project row
// lock and the permission-loss tombstone. In a project in its restore window only removed_at is
// set: the project's own deletion purges what it derived, and a restore must not bring the account
// back. Owned projects are deleted separately.
func (s *Store) RemoveAccountMemberships(ctx context.Context, userID string, removedAt time.Time) (int, error) {
	user, err := storedb.ParseUUID(userID)
	if err != nil {
		return 0, projects.ErrValidation
	}
	removed := 0
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		type membership struct {
			projectID  pgtype.UUID
			generation int64
			deleted    bool
			id         pgtype.UUID
		}
		rows, err := tx.Query(ctx, `
			SELECT project.id, project.lifecycle_generation, project.deleted_at IS NOT NULL, member.id
			FROM projects AS project
			JOIN project_memberships AS member ON member.project_id = project.id
			WHERE member.user_id = $1
			  AND member.removed_at IS NULL
			  AND member.role <> 'owner'
			ORDER BY project.id
			FOR UPDATE OF project, member
		`, user)
		if err != nil {
			return err
		}
		var memberships []membership
		for rows.Next() {
			var row membership
			if err := rows.Scan(&row.projectID, &row.generation, &row.deleted, &row.id); err != nil {
				rows.Close()
				return err
			}
			memberships = append(memberships, row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, row := range memberships {
			var updatedAt pgtype.Timestamptz
			if err := tx.QueryRow(ctx, `
				UPDATE project_memberships
				SET removed_at = $1, updated_at = $1
				WHERE id = $2 AND project_id = $3 AND removed_at IS NULL
				RETURNING updated_at
			`, storedb.Timestamptz(removedAt.UTC().Truncate(time.Microsecond)), row.id, row.projectID).Scan(&updatedAt); errors.Is(err, pgx.ErrNoRows) {
				return projects.ErrConflict
			} else if err != nil {
				return err
			}
			if !row.deleted {
				if err := enqueuePermissionLossDeletion(ctx, tx, row.projectID, userID, row.generation, updatedAt.Time); err != nil {
					return err
				}
			}
			removed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

package deletion

import (
	"context"

	deletion "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"

	"github.com/jackc/pgx/v5"
)

// InsertPostgresStepRegistry сохраняет список шагов PostgreSQL-очистки для tombstone.
func InsertPostgresStepRegistry(
	ctx context.Context,
	tx pgx.Tx,
	tombstoneID string,
	projectID string,
	kind governance.DeletionSelectionKind,
	mode governance.PurgeMode,
) error {
	steps, err := deletion.PostgresStepRegistry(kind, mode)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_target_step_progress (
				tombstone_id, project_id, target, step_name, step_order,
				cursor_kind, state, processed_count
			) VALUES ($1, $2, 'postgresql', $3, $4, $5, 'pending', 0)
		`, tombstoneID, projectID, step.Name, step.Order, string(step.CursorKind)); err != nil {
			return err
		}
	}
	return nil
}

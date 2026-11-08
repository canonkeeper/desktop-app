package deletion

import (
	"context"
	"fmt"

	deletionservice "canonkeeper/backend/internal/services/deletion"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// tombstoneSourceDeletedModelJobsInTx выбирает bound model Jobs точным
// вектором источника (тот же предикат, что и rejectPostgresBindingOnlyModelCleanup)
// и передаёт каждый владельцу tombstone в stable job-id порядке. Прогресс-cursor
// не нужен: tombstoned Jobs теряют owner/binding и выходят из выборки.
func tombstoneSourceDeletedModelJobsInTx(
	ctx context.Context,
	tx pgx.Tx,
	selection postgresDeletionSelection,
	tombstoner SourceDeletedModelJobTombstoner,
) error {
	query := fmt.Sprintf(`
		SELECT job.id
		FROM jobs AS job
		JOIN artifact_source_owners AS owner
		  ON owner.id = job.artifact_source_owner_id
		 AND owner.project_id = job.project_id
		JOIN job_model_bindings AS binding
		  ON binding.project_id = job.project_id
		 AND binding.job_id = job.id
		WHERE job.project_id = $1::uuid
		  AND owner.artifact_kind = 'job'
		  AND %s
		ORDER BY job.id
	`, postgresOwnerSelectionPredicate("owner"))
	rows, err := tx.Query(ctx, query, selection.predicateArgs()...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var jobIDs []pgtype.UUID
	for rows.Next() {
		var jobID pgtype.UUID
		if err := rows.Scan(&jobID); err != nil {
			return err
		}
		if !jobID.Valid {
			return deletionservice.ErrPostgresTargetConflict
		}
		jobIDs = append(jobIDs, jobID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, jobID := range jobIDs {
		if err := tombstoner.TombstoneSourceDeletedModelJobInTx(ctx, tx, selection.projectID, jobID.String()); err != nil {
			return fmt.Errorf("PostgreSQL deletion tombstone pre-pass job %s: %w", jobID.String(), err)
		}
	}
	return nil
}

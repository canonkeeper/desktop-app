-- name: GetProjectScopedJobFixture :one
SELECT id, project_id, kind, status
FROM jobs
WHERE project_id = sqlc.arg(project_id)
  AND id = sqlc.arg(job_id);

-- name: ClaimNextDeletionTargetTaskForWorker :one
SELECT *
FROM canonkeeper_worker.claim_next_deletion_target_task_for_worker(
  sqlc.arg(claimed_by_digest), sqlc.arg(lease_seconds)
);

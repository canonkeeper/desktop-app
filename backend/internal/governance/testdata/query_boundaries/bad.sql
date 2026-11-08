-- name: GetTenantlessJobFixture :one
SELECT id, result_json
FROM jobs
WHERE id = sqlc.arg(job_id);

-- name: UnregisteredActorLookup :one
SELECT p.name
FROM projects p
JOIN project_memberships m ON m.project_id = p.id
WHERE m.user_id = sqlc.arg(actor_user_id);

-- name: ResolveBookAccessForActor :one
SELECT b.project_id, b.id AS book_id, b.title, m.role
FROM books b
JOIN project_memberships m ON m.project_id = b.project_id
WHERE b.id = sqlc.arg(book_id)
  AND m.user_id = sqlc.arg(actor_user_id);

-- name: ClaimNextDeletionTargetTaskForWorker :one
UPDATE deletion_target_tasks
SET state = 'processing'
WHERE tombstone_id = (
  SELECT tombstone_id FROM deletion_target_tasks LIMIT 1
)
RETURNING project_id, tombstone_id;

-- name: RevokeExpiredSessions :execrows
WITH db_clock AS MATERIALIZED (
  SELECT clock_timestamp() AS now_at
), candidates AS MATERIALIZED (
  SELECT session.id
  FROM sessions AS session
  CROSS JOIN db_clock
  WHERE session.expires_at <= db_clock.now_at
    AND session.revoked_at IS NULL
  ORDER BY session.expires_at, session.id
  LIMIT 100
  FOR UPDATE OF session SKIP LOCKED
)
UPDATE sessions AS session
SET revoked_at = db_clock.now_at
FROM candidates, db_clock
WHERE session.id = candidates.id
  AND session.revoked_at IS NULL;

-- name: MarkRetentionDeadlineBreaches :execrows
WITH db_clock AS MATERIALIZED (
  SELECT clock_timestamp() AS now_at
), candidates AS MATERIALIZED (
  SELECT tombstone.id, tombstone.project_id
  FROM deletion_tombstones AS tombstone
  CROSS JOIN db_clock
  WHERE tombstone.origin = 'governed_online'
    AND tombstone.status <> 'purged'
    AND tombstone.purge_deadline <= db_clock.now_at
    AND tombstone.retention_deadline_breached_at IS NULL
  ORDER BY tombstone.purge_deadline, tombstone.id, tombstone.project_id
  LIMIT 100
  FOR UPDATE OF tombstone SKIP LOCKED
)
UPDATE deletion_tombstones AS tombstone
SET retention_deadline_breached_at = db_clock.now_at,
    updated_at = db_clock.now_at
FROM candidates, db_clock
WHERE tombstone.id = candidates.id
  AND tombstone.project_id = candidates.project_id
  AND tombstone.status <> 'purged'
  AND tombstone.retention_deadline_breached_at IS NULL;

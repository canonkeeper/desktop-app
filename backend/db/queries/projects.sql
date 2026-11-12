-- name: CreateProject :one
INSERT INTO projects (
  id,
  owner_user_id,
  name,
  description,
  demo,
  lifecycle_generation,
  created_at,
  updated_at
) VALUES (
  sqlc.arg(id),
  sqlc.arg(owner_user_id),
  sqlc.arg(name),
  sqlc.arg(description),
  sqlc.arg(demo),
  1,
  sqlc.arg(created_at),
  sqlc.arg(created_at)
)
RETURNING id, owner_user_id, name, description, demo,
          0::integer AS book_count,
          0::integer AS chapter_count,
          0::integer AS word_count,
          revision, created_at, updated_at;

-- name: CreateProjectMembership :one
INSERT INTO project_memberships (
  id,
  project_id,
  user_id,
  role,
  created_at,
  updated_at
) VALUES (
  sqlc.arg(id),
  sqlc.arg(project_id),
  sqlc.arg(user_id),
  sqlc.arg(role),
  sqlc.arg(created_at),
  sqlc.arg(created_at)
)
RETURNING id, project_id, user_id, role, removed_at, created_at, updated_at;

-- name: UpsertProjectInvitationMembership :one
INSERT INTO project_memberships (
  id,
  project_id,
  user_id,
  role,
  created_at,
  updated_at
) VALUES (
  sqlc.arg(id),
  sqlc.arg(project_id),
  sqlc.arg(user_id),
  sqlc.arg(role),
  sqlc.arg(created_at),
  sqlc.arg(created_at)
)
ON CONFLICT (project_id, user_id) DO UPDATE
SET
  role = EXCLUDED.role,
  removed_at = NULL,
  updated_at = EXCLUDED.updated_at
WHERE project_memberships.removed_at IS NOT NULL
RETURNING id, project_id, user_id, role, removed_at, created_at, updated_at;

-- name: GetActiveProjectRoleForActor :one
SELECT m.role
FROM project_memberships m
JOIN projects p ON p.id = m.project_id
WHERE m.user_id = sqlc.arg(actor_user_id)
  AND m.project_id = sqlc.arg(project_id)
  AND m.removed_at IS NULL
  AND p.deleted_at IS NULL;

-- name: GetProjectForActor :one
SELECT
  p.id,
  p.owner_user_id,
  p.name,
  p.description,
  p.demo,
  project_counts.book_count,
  project_counts.chapter_count,
  project_counts.word_count,
  p.revision,
  p.created_at,
  p.updated_at,
  m.id AS membership_id,
  m.project_id AS membership_project_id,
  m.user_id AS membership_user_id,
  m.role AS membership_role,
  m.removed_at AS membership_removed_at,
  m.created_at AS membership_created_at,
  m.updated_at AS membership_updated_at
FROM projects p
JOIN project_memberships m ON m.project_id = p.id
LEFT JOIN LATERAL (
  SELECT
    COUNT(DISTINCT b.id)::integer AS book_count,
    COUNT(DISTINCT c.id)::integer AS chapter_count,
    COALESCE(SUM(cp.word_count), 0)::integer AS word_count
  FROM books b
  LEFT JOIN chapters c ON c.book_id = b.id AND c.deleted_at IS NULL
  LEFT JOIN chapter_paragraphs cp
    ON cp.chapter_id = c.id
    AND cp.revision = c.revision
    AND cp.variant = 'draft'
  WHERE b.project_id = p.id
    AND b.deleted_at IS NULL
) project_counts ON true
WHERE p.id = sqlc.arg(project_id)
  AND p.deleted_at IS NULL
  AND m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL;

-- name: ListProjectsForActor :many
SELECT
  p.id,
  p.name,
  p.description,
  project_counts.book_count,
  project_counts.chapter_count,
  project_counts.word_count,
  p.created_at,
  p.updated_at,
  m.id AS membership_id,
  m.project_id AS membership_project_id,
  m.user_id AS membership_user_id,
  m.role AS membership_role,
  m.removed_at AS membership_removed_at,
  m.created_at AS membership_created_at,
  m.updated_at AS membership_updated_at
FROM projects p
JOIN project_memberships m ON m.project_id = p.id
LEFT JOIN LATERAL (
  SELECT
    COUNT(DISTINCT b.id)::integer AS book_count,
    COUNT(DISTINCT c.id)::integer AS chapter_count,
    COALESCE(SUM(cp.word_count), 0)::integer AS word_count
  FROM books b
  LEFT JOIN chapters c ON c.book_id = b.id AND c.deleted_at IS NULL
  LEFT JOIN chapter_paragraphs cp
    ON cp.chapter_id = c.id
    AND cp.revision = c.revision
    AND cp.variant = 'draft'
  WHERE b.project_id = p.id
    AND b.deleted_at IS NULL
) project_counts ON true
WHERE p.deleted_at IS NULL
  AND m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL
  AND (
    NOT sqlc.arg(has_cursor)::boolean
    OR (p.created_at, p.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
  )
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg(limit_count);

-- name: UpdateProjectForActor :one
WITH updated_project AS (
  UPDATE projects p
  SET
    name = sqlc.arg(name),
    description = sqlc.arg(description),
    revision = p.revision + 1,
    updated_at = sqlc.arg(updated_at)
  FROM project_memberships m
  WHERE p.id = sqlc.arg(project_id)
    AND p.deleted_at IS NULL
    AND m.project_id = p.id
    AND m.user_id = sqlc.arg(actor_user_id)
    AND m.removed_at IS NULL
    AND m.role IN ('editor', 'admin', 'owner')
  RETURNING
    p.id,
    p.owner_user_id,
    p.name,
    p.description,
    p.demo,
    p.revision,
    p.created_at,
    p.updated_at,
    m.id AS membership_id,
    m.project_id AS membership_project_id,
    m.user_id AS membership_user_id,
    m.role AS membership_role,
    m.removed_at AS membership_removed_at,
    m.created_at AS membership_created_at,
    m.updated_at AS membership_updated_at
)
SELECT
  updated_project.id,
  updated_project.owner_user_id,
  updated_project.name,
  updated_project.description,
  updated_project.demo,
  project_counts.book_count,
  project_counts.chapter_count,
  project_counts.word_count,
  updated_project.revision,
  updated_project.created_at,
  updated_project.updated_at,
  updated_project.membership_id,
  updated_project.membership_project_id,
  updated_project.membership_user_id,
  updated_project.membership_role,
  updated_project.membership_removed_at,
  updated_project.membership_created_at,
  updated_project.membership_updated_at
FROM updated_project
LEFT JOIN LATERAL (
  SELECT
    COUNT(DISTINCT b.id)::integer AS book_count,
    COUNT(DISTINCT c.id)::integer AS chapter_count,
    COALESCE(SUM(cp.word_count), 0)::integer AS word_count
  FROM books b
  LEFT JOIN chapters c ON c.book_id = b.id AND c.deleted_at IS NULL
  LEFT JOIN chapter_paragraphs cp
    ON cp.chapter_id = c.id
    AND cp.revision = c.revision
    AND cp.variant = 'draft'
  WHERE b.project_id = updated_project.id
    AND b.deleted_at IS NULL
) project_counts ON true;

-- name: ListProjectMembersForActor :many
SELECT
  target.id,
  target.project_id,
  target.user_id,
  member_user.email,
  member_user.display_name,
  target.role,
  target.removed_at,
  target.created_at,
  target.updated_at
FROM projects p
JOIN project_memberships actor ON actor.project_id = p.id
JOIN project_memberships target ON target.project_id = p.id
JOIN users member_user ON member_user.id = target.user_id
WHERE p.id = sqlc.arg(project_id)
  AND p.deleted_at IS NULL
  AND actor.user_id = sqlc.arg(actor_user_id)
  AND actor.removed_at IS NULL
  AND actor.role IN ('admin', 'owner')
  AND target.removed_at IS NULL
ORDER BY target.created_at ASC, target.id ASC
LIMIT sqlc.arg(limit_count);

-- name: UpdateProjectMemberRoleForActor :one
WITH updated AS (
  UPDATE project_memberships target
  SET
    role = sqlc.arg(role),
    updated_at = sqlc.arg(updated_at)
  FROM projects p
  JOIN project_memberships actor ON actor.project_id = p.id
  WHERE target.id = sqlc.arg(member_id)
    AND target.project_id = p.id
    AND p.id = sqlc.arg(project_id)
    AND p.deleted_at IS NULL
    AND target.removed_at IS NULL
    AND target.role <> 'owner'
    AND actor.user_id = sqlc.arg(actor_user_id)
    AND actor.removed_at IS NULL
    AND actor.role IN ('admin', 'owner')
  RETURNING target.id, target.project_id, target.user_id, target.role, target.removed_at, target.created_at, target.updated_at
)
SELECT
  updated.id,
  updated.project_id,
  updated.user_id,
  member_user.email,
  member_user.display_name,
  updated.role,
  updated.removed_at,
  updated.created_at,
  updated.updated_at
FROM updated
JOIN users member_user ON member_user.id = updated.user_id;

-- name: ExpireProjectInvitationCreateConflicts :exec
UPDATE project_invitations i
SET
  status = 'expired',
  updated_at = sqlc.arg(created_at)
FROM projects p
JOIN project_memberships m ON m.project_id = p.id
WHERE i.project_id = p.id
  AND i.email_hash = sqlc.arg(email_hash)
  AND i.status = 'pending'
  AND i.accepted_at IS NULL
  AND i.canceled_at IS NULL
  AND i.expires_at <= sqlc.arg(created_at)
  AND p.id = sqlc.arg(project_id)
  AND p.deleted_at IS NULL
  AND m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL
  AND m.role IN ('admin', 'owner');

-- name: CreateProjectInvitation :one
WITH inserted AS (
  INSERT INTO project_invitations (
    id,
    project_id,
    inviter_user_id,
    email,
    email_hash,
    role,
    status,
    expires_at,
    created_at,
    updated_at
  )
  SELECT
    sqlc.arg(id),
    p.id,
    sqlc.arg(actor_user_id),
    sqlc.arg(email),
    sqlc.arg(email_hash),
    sqlc.arg(role),
    'pending',
    sqlc.arg(expires_at),
    sqlc.arg(created_at),
    sqlc.arg(created_at)
  FROM projects p
  JOIN project_memberships m ON m.project_id = p.id
  WHERE p.id = sqlc.arg(project_id)
    AND p.deleted_at IS NULL
    AND m.user_id = sqlc.arg(actor_user_id)
    AND m.removed_at IS NULL
    AND m.role IN ('admin', 'owner')
    AND NOT EXISTS (
      SELECT 1
      FROM project_invitations existing
      WHERE existing.project_id = p.id
        AND existing.email_hash = sqlc.arg(email_hash)
        AND existing.status = 'pending'
        AND existing.accepted_at IS NULL
        AND existing.canceled_at IS NULL
        AND existing.expires_at > sqlc.arg(created_at)
    )
    AND NOT EXISTS (
      SELECT 1
      FROM project_memberships existing_membership
      JOIN users existing_user ON existing_user.id = existing_membership.user_id
      WHERE existing_membership.project_id = p.id
        AND existing_membership.removed_at IS NULL
        AND existing_user.email = sqlc.arg(email)
    )
  RETURNING id, project_id, inviter_user_id, email, email_hash, role, status, expires_at, accepted_at, canceled_at, created_at, updated_at
)
SELECT
  i.id,
  i.project_id,
  p.name AS project_title,
  i.inviter_user_id,
  inviter.display_name AS inviter_display_name,
  inviter.email AS inviter_email,
  i.email,
  i.email_hash,
  i.role,
  i.status,
  i.expires_at,
  i.accepted_at,
  i.canceled_at,
  i.created_at,
  i.updated_at
FROM inserted i
JOIN projects p ON p.id = i.project_id
JOIN users inviter ON inviter.id = i.inviter_user_id;

-- name: ListProjectInvitations :many
SELECT
  i.id,
  i.project_id,
  p.name AS project_title,
  i.inviter_user_id,
  inviter.display_name AS inviter_display_name,
  inviter.email AS inviter_email,
  i.email,
  i.email_hash,
  i.role,
  i.status,
  i.expires_at,
  i.accepted_at,
  i.canceled_at,
  i.created_at,
  i.updated_at
FROM project_invitations i
JOIN projects p ON p.id = i.project_id
JOIN users inviter ON inviter.id = i.inviter_user_id
JOIN project_memberships m ON m.project_id = p.id
WHERE i.project_id = sqlc.arg(project_id)
  AND p.deleted_at IS NULL
  AND m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL
  AND m.role IN ('admin', 'owner')
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(limit_count);

-- name: ResolveProjectInvitationMemberAccessForActor :one
SELECT
  i.project_id,
  i.id AS invitation_id,
  m.role AS project_role
FROM project_invitations i
JOIN projects p ON p.id = i.project_id
JOIN project_memberships m ON m.project_id = p.id
WHERE i.id = sqlc.arg(invitation_id)
  AND p.deleted_at IS NULL
  AND m.user_id = sqlc.arg(actor_user_id)
  AND m.removed_at IS NULL;

-- name: ResolveProjectInvitationInviteeAccessForActor :one
SELECT
  i.project_id,
  i.id AS invitation_id
FROM project_invitations i
JOIN projects p ON p.id = i.project_id
JOIN users invitee ON invitee.id = sqlc.arg(actor_user_id)
WHERE i.id = sqlc.arg(invitation_id)
  AND i.email_hash = sqlc.arg(email_hash)
  AND i.email = lower(btrim(invitee.email))
  AND invitee.email_verified_at IS NOT NULL
  AND invitee.disabled_at IS NULL
  AND p.deleted_at IS NULL;

-- name: GetProjectInvitationForInviteeForUpdate :one
SELECT
  i.id,
  i.project_id,
  p.name AS project_title,
  i.inviter_user_id,
  inviter.display_name AS inviter_display_name,
  inviter.email AS inviter_email,
  i.email,
  i.email_hash,
  i.role,
  i.status,
  i.expires_at,
  i.accepted_at,
  i.canceled_at,
  i.created_at,
  i.updated_at
FROM project_invitations i
JOIN projects p ON p.id = i.project_id
JOIN users inviter ON inviter.id = i.inviter_user_id
JOIN users invitee ON invitee.id = sqlc.arg(user_id)
WHERE i.id = sqlc.arg(invitation_id)
  AND p.id = sqlc.arg(project_id)
  AND i.email_hash = sqlc.arg(email_hash)
  AND i.email = lower(btrim(invitee.email))
  AND invitee.email_verified_at IS NOT NULL
  AND invitee.disabled_at IS NULL
  AND p.deleted_at IS NULL
FOR UPDATE OF i, p;

-- name: ListProjectInvitationsForActor :many
SELECT
  i.id,
  i.project_id,
  p.name AS project_title,
  inviter.display_name AS inviter_display_name,
  inviter.email AS inviter_email,
  i.email,
  i.role,
  i.status,
  i.expires_at,
  i.accepted_at,
  i.created_at,
  i.updated_at
FROM project_invitations i
JOIN projects p ON p.id = i.project_id
JOIN users inviter ON inviter.id = i.inviter_user_id
JOIN users invitee ON invitee.id = sqlc.arg(actor_user_id)
WHERE i.email_hash = sqlc.arg(email_hash)
  AND i.email = lower(btrim(invitee.email))
  AND invitee.email_verified_at IS NOT NULL
  AND invitee.disabled_at IS NULL
  AND p.deleted_at IS NULL
  AND (
    NOT sqlc.arg(has_cursor)::boolean
    OR (i.created_at, i.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
  )
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(limit_count);

-- name: CancelProjectInvitation :one
WITH updated AS (
  UPDATE project_invitations pi
  SET
    status = 'canceled',
    canceled_at = sqlc.arg(canceled_at),
    updated_at = sqlc.arg(canceled_at)
  FROM projects p
  JOIN project_memberships m ON m.project_id = p.id
  WHERE pi.id = sqlc.arg(invitation_id)
    AND pi.project_id = p.id
    AND p.id = sqlc.arg(project_id)
    AND p.deleted_at IS NULL
    AND m.project_id = p.id
    AND m.user_id = sqlc.arg(actor_user_id)
    AND m.removed_at IS NULL
    AND m.role IN ('admin', 'owner')
    AND pi.status = 'pending'
    AND pi.accepted_at IS NULL
    AND pi.canceled_at IS NULL
    AND pi.expires_at > sqlc.arg(canceled_at)
  RETURNING pi.id, pi.project_id, pi.inviter_user_id, pi.email, pi.email_hash, pi.role, pi.status, pi.expires_at, pi.accepted_at, pi.canceled_at, pi.created_at, pi.updated_at
)
SELECT
  i.id,
  i.project_id,
  p.name AS project_title,
  i.inviter_user_id,
  inviter.display_name AS inviter_display_name,
  inviter.email AS inviter_email,
  i.email,
  i.email_hash,
  i.role,
  i.status,
  i.expires_at,
  i.accepted_at,
  i.canceled_at,
  i.created_at,
  i.updated_at
FROM updated i
JOIN projects p ON p.id = i.project_id
JOIN users inviter ON inviter.id = i.inviter_user_id;

-- name: MarkProjectInvitationAccepted :one
WITH updated AS (
  UPDATE project_invitations pi
  SET
    status = 'accepted',
    accepted_at = sqlc.arg(accepted_at),
    updated_at = sqlc.arg(accepted_at)
  FROM projects p
  JOIN users invitee ON invitee.id = sqlc.arg(user_id)
  WHERE pi.id = sqlc.arg(invitation_id)
    AND pi.project_id = p.id
    AND p.id = sqlc.arg(project_id)
    AND pi.email_hash = sqlc.arg(email_hash)
    AND pi.email = lower(btrim(invitee.email))
    AND invitee.email_verified_at IS NOT NULL
    AND invitee.disabled_at IS NULL
    AND p.deleted_at IS NULL
    AND pi.status = 'pending'
    AND pi.accepted_at IS NULL
    AND pi.canceled_at IS NULL
    AND pi.expires_at > sqlc.arg(accepted_at)
  RETURNING pi.id, pi.project_id, pi.inviter_user_id, pi.email, pi.email_hash, pi.role, pi.status, pi.expires_at, pi.accepted_at, pi.canceled_at, pi.created_at, pi.updated_at
)
SELECT
  i.id,
  i.project_id,
  p.name AS project_title,
  i.inviter_user_id,
  inviter.display_name AS inviter_display_name,
  inviter.email AS inviter_email,
  i.email,
  i.email_hash,
  i.role,
  i.status,
  i.expires_at,
  i.accepted_at,
  i.canceled_at,
  i.created_at,
  i.updated_at
FROM updated i
JOIN projects p ON p.id = i.project_id
JOIN users inviter ON inviter.id = i.inviter_user_id;

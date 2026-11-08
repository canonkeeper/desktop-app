-- name: CreateArtifactSourceOwner :exec
INSERT INTO artifact_source_owners (
  id,
  project_id,
  artifact_kind,
  artifact_id,
  source_view,
  manifest_version,
  manifest_hash,
  ref_count,
  read_state,
  source_identity_state
)
VALUES (
  sqlc.arg(id),
  sqlc.arg(project_id),
  sqlc.arg(artifact_kind),
  sqlc.arg(artifact_id),
  sqlc.narg(source_view),
  sqlc.arg(manifest_version),
  sqlc.arg(manifest_hash),
  sqlc.arg(ref_count),
  'active',
  sqlc.arg(source_identity_state)
);

-- name: CreateArtifactSourceRefs :batchexec
INSERT INTO artifact_source_refs (
  id,
  owner_id,
  project_id,
  ref_kind,
  scope_kind,
  source_view,
  project_generation,
  book_id,
  book_generation,
  chapter_id,
  chapter_generation,
  source_revision,
  content_hash,
  locator_source_generation,
  text_identity_state,
  source_independent,
  chat_session_id,
  chat_lifecycle_epoch
)
VALUES (
  COALESCE(sqlc.narg(id)::uuid, gen_random_uuid()),
  sqlc.arg(owner_id),
  sqlc.arg(project_id),
  sqlc.arg(ref_kind),
  sqlc.narg(scope_kind),
  sqlc.narg(source_view),
  sqlc.arg(project_generation),
  sqlc.narg(book_id),
  sqlc.narg(book_generation),
  sqlc.narg(chapter_id),
  sqlc.narg(chapter_generation),
  sqlc.narg(source_revision),
  sqlc.narg(content_hash),
  sqlc.narg(locator_source_generation),
  sqlc.narg(text_identity_state),
  sqlc.arg(source_independent),
  sqlc.narg(chat_session_id),
  sqlc.narg(chat_lifecycle_epoch)
);

-- name: ReplaceArtifactSourceOwnerManifest :one
UPDATE artifact_source_owners
SET
  source_view = sqlc.narg(source_view),
  manifest_version = sqlc.arg(new_manifest_version),
  manifest_hash = sqlc.arg(new_manifest_hash),
  ref_count = sqlc.arg(new_ref_count),
  source_identity_state = sqlc.arg(source_identity_state),
  updated_at = clock_timestamp()
WHERE id = sqlc.arg(owner_id)
  AND project_id = sqlc.arg(project_id)
  AND artifact_kind = sqlc.arg(artifact_kind)
  AND artifact_id = sqlc.arg(artifact_id)
  AND manifest_version = sqlc.arg(expected_manifest_version)
  AND manifest_hash = sqlc.arg(expected_manifest_hash)
  AND read_state = 'active'
  AND fenced_at IS NULL
RETURNING id;

-- name: ReadArtifactSourceTransferOwner :one
SELECT
  owner.id,
  owner.project_id,
  owner.artifact_kind,
  owner.artifact_id,
  owner.source_view,
  owner.manifest_version,
  owner.manifest_hash,
  owner.ref_count,
  owner.read_state,
  owner.source_identity_state,
  owner.fenced_at,
  owner.project_id = sqlc.arg(project_id)::uuid AS project_matches,
  owner.artifact_kind = sqlc.arg(artifact_kind)::text AS kind_matches,
  owner.artifact_id = sqlc.arg(artifact_id)::uuid AS artifact_matches
FROM artifact_source_owners AS owner
WHERE owner.id = sqlc.arg(owner_id);

-- name: ListArtifactSourceTransferRefs :many
SELECT ref.*
FROM artifact_source_refs AS ref
WHERE ref.owner_id = sqlc.arg(owner_id)
  AND ref.project_id = sqlc.arg(project_id)
ORDER BY
  CASE
    WHEN ref.scope_kind = 'project' THEN 1
    WHEN ref.scope_kind = 'book' THEN 2
    WHEN ref.scope_kind = 'chapter' THEN 3
    WHEN ref.ref_kind = 'chapter' THEN 4
  END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.project_id END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.project_generation END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.book_id END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.book_generation END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.chapter_id END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.chapter_generation END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.source_revision END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.source_view END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.locator_source_generation END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.content_hash END,
  CASE WHEN ref.ref_kind = 'chapter' AND ref.chat_session_id IS NOT NULL THEN 1 ELSE 0 END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.chat_session_id END,
  CASE WHEN ref.ref_kind = 'chapter' THEN ref.chat_lifecycle_epoch END,
  CASE WHEN ref.ref_kind = 'scope' THEN ref.project_id END,
  CASE WHEN ref.ref_kind = 'scope' THEN ref.project_generation END,
  CASE WHEN ref.ref_kind = 'scope' THEN ref.book_id END,
  CASE WHEN ref.ref_kind = 'scope' THEN ref.book_generation END,
  CASE WHEN ref.ref_kind = 'scope' THEN ref.chapter_id END,
  CASE WHEN ref.ref_kind = 'scope' THEN ref.chapter_generation END,
  CASE
    WHEN ref.ref_kind = 'scope' AND ref.source_view IS NULL THEN 0
    WHEN ref.ref_kind = 'scope' AND ref.source_view = 'draft' THEN 1
    WHEN ref.ref_kind = 'scope' THEN 2
  END,
  CASE WHEN ref.ref_kind = 'scope' AND ref.chat_session_id IS NOT NULL THEN 1 ELSE 0 END,
  CASE WHEN ref.ref_kind = 'scope' THEN ref.chat_session_id END,
  CASE WHEN ref.ref_kind = 'scope' THEN ref.chat_lifecycle_epoch END;

-- name: LockArtifactSourceTransferOwner :one
SELECT owner.id
FROM artifact_source_owners AS owner
WHERE owner.id = sqlc.arg(owner_id)
  AND owner.project_id = sqlc.arg(project_id)
  AND owner.artifact_kind = sqlc.arg(artifact_kind)
  AND owner.artifact_id = sqlc.arg(artifact_id)
  AND owner.source_view IS NOT DISTINCT FROM sqlc.narg(source_view)
  AND owner.manifest_version = sqlc.arg(manifest_version)
  AND owner.manifest_hash = sqlc.arg(manifest_hash)
  AND owner.ref_count = sqlc.arg(ref_count)
  AND owner.source_identity_state = sqlc.arg(source_identity_state)
  AND owner.read_state = 'active'
  AND owner.fenced_at IS NULL
FOR SHARE;

-- name: LockArtifactSourceOwnerForDelete :one
SELECT id
FROM artifact_source_owners
WHERE id = sqlc.arg(owner_id)
  AND project_id = sqlc.arg(project_id)
  AND artifact_kind = sqlc.arg(artifact_kind)
  AND artifact_id = sqlc.arg(artifact_id)
  AND manifest_version = sqlc.arg(expected_manifest_version)
  AND manifest_hash = sqlc.arg(expected_manifest_hash)
FOR UPDATE;

-- name: SealArtifactSourceOwner :one
UPDATE artifact_source_owners
SET updated_at = clock_timestamp()
WHERE id = sqlc.arg(owner_id)
  AND project_id = sqlc.arg(project_id)
  AND artifact_kind = sqlc.arg(artifact_kind)
  AND artifact_id = sqlc.arg(artifact_id)
  AND source_view IS NOT DISTINCT FROM sqlc.narg(source_view)
  AND manifest_version = sqlc.arg(manifest_version)
  AND manifest_hash = sqlc.arg(manifest_hash)
  AND ref_count = sqlc.arg(ref_count)
  AND source_identity_state = sqlc.arg(source_identity_state)
  AND read_state = 'active'
  AND fenced_at IS NULL
RETURNING id;

-- name: DeleteArtifactSourceRefs :execrows
DELETE FROM artifact_source_refs
WHERE owner_id = sqlc.arg(owner_id)
  AND project_id = sqlc.arg(project_id);

-- name: DeleteArtifactSourceOwner :execrows
DELETE FROM artifact_source_owners
WHERE id = sqlc.arg(owner_id)
  AND project_id = sqlc.arg(project_id)
  AND artifact_kind = sqlc.arg(artifact_kind)
  AND artifact_id = sqlc.arg(artifact_id)
  AND manifest_version = sqlc.arg(expected_manifest_version)
  AND manifest_hash = sqlc.arg(expected_manifest_hash);

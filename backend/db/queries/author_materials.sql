-- name: ListAuthorMaterials :many
WITH filtered AS (
  SELECT record.id, record.project_id, record.name, record.name_key, record.kind, record.description, record.properties,
         record.section, record.source_name, record.source_kind, record.revision, record.created_at, record.updated_at
  FROM author_materials AS record
  WHERE record.project_id = sqlc.arg(project_id)
    AND (sqlc.arg(kinds)::text[] = '{}' OR record.kind = ANY(sqlc.arg(kinds)::text[]))
    AND (sqlc.arg(search_query)::text = ''
      OR strpos(lower(record.name), lower(sqlc.arg(search_query))) > 0
      OR strpos(lower(record.description), lower(sqlc.arg(search_query))) > 0)
)
SELECT record.id, record.project_id, record.name, record.name_key, record.kind, record.description, record.properties,
       record.section, record.source_name, record.source_kind, record.revision, record.created_at, record.updated_at,
       (SELECT count(*) FROM filtered) AS total_count
FROM filtered AS record
WHERE record.name_key > sqlc.arg(after_key)::text
ORDER BY record.name_key
LIMIT sqlc.arg(limit_count);

-- name: AllAuthorMaterials :many
SELECT id, project_id, name, name_key, kind, description, properties, section, source_name, source_kind, revision, created_at, updated_at
FROM author_materials
WHERE project_id = sqlc.arg(project_id)
ORDER BY name_key
LIMIT sqlc.arg(limit_count);

-- name: GetAuthorMaterial :one
SELECT id, project_id, name, name_key, kind, description, properties, section, source_name, source_kind, revision, created_at, updated_at
FROM author_materials
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: AuthorMaterialKeys :many
SELECT name_key FROM author_materials WHERE project_id = sqlc.arg(project_id);

-- name: LockAuthorMaterialRevision :one
SELECT revision FROM author_materials
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: UpdateAuthorMaterial :execrows
UPDATE author_materials
SET name = sqlc.arg(name), name_key = sqlc.arg(name_key), kind = sqlc.arg(kind), description = sqlc.arg(description),
    properties = sqlc.arg(properties)::jsonb, section = sqlc.arg(section), revision = revision + 1,
    updated_by = sqlc.arg(actor), updated_at = clock_timestamp()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision);

-- name: DeleteAuthorMaterial :execrows
DELETE FROM author_materials
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision);

-- name: UpsertAuthorMaterial :exec
INSERT INTO author_materials (
  project_id, name, name_key, kind, description, properties, section, source_name, source_kind, created_by, updated_by
)
VALUES (
  sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(name_key), sqlc.arg(kind), sqlc.arg(description), sqlc.arg(properties)::jsonb,
  sqlc.arg(section), sqlc.arg(source_name), sqlc.arg(source_kind), sqlc.arg(actor), sqlc.arg(actor)
)
ON CONFLICT (project_id, name_key) DO UPDATE
SET name = EXCLUDED.name, kind = EXCLUDED.kind, description = EXCLUDED.description, properties = EXCLUDED.properties,
    section = EXCLUDED.section, source_name = EXCLUDED.source_name, source_kind = EXCLUDED.source_kind,
    revision = author_materials.revision + 1, updated_by = EXCLUDED.updated_by, updated_at = clock_timestamp();

-- name: GetAuthorMaterialImport :one
SELECT import.id, import.project_id, import.job_id, import.source_name, import.source_kind, import.candidates, import.created_at,
       job.status AS job_status
FROM author_material_imports AS import
JOIN jobs AS job ON job.id = import.job_id AND job.project_id = import.project_id
WHERE import.project_id = sqlc.arg(project_id);

-- name: LockAuthorMaterialImport :one
SELECT import.id, import.job_id, import.source_name, import.source_kind, import.candidates, job.status AS job_status
FROM author_material_imports AS import
JOIN jobs AS job ON job.id = import.job_id AND job.project_id = import.project_id
WHERE import.project_id = sqlc.arg(project_id)
FOR UPDATE OF import;

-- name: CreateAuthorMaterialImport :exec
INSERT INTO author_material_imports (id, project_id, job_id, source_name, source_kind, parsed, created_by)
VALUES (sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(job_id), sqlc.arg(source_name), sqlc.arg(source_kind), sqlc.arg(parsed)::jsonb, sqlc.arg(actor));

-- name: DeleteAuthorMaterialImport :execrows
DELETE FROM author_material_imports WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: DeleteAuthorMaterialImportOfJob :exec
DELETE FROM author_material_imports WHERE project_id = sqlc.arg(project_id) AND job_id = sqlc.arg(job_id);

-- name: GetAuthorMaterialImportParsed :one
SELECT parsed FROM author_material_imports
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND job_id = sqlc.arg(job_id);

-- The file has served once its Job ends: only the candidates, if any, stay.
-- name: EndAuthorMaterialImport :execrows
UPDATE author_material_imports SET candidates = sqlc.narg(candidates)::jsonb, parsed = '{}'::jsonb
WHERE project_id = sqlc.arg(project_id) AND job_id = sqlc.arg(job_id);

-- name: DeleteSagaMapNodesOfProject :exec
-- A deleted record leaves no text of it in the prepared cards; the graph stays until the next build (specs/2026-11-04-saga-map.md, R9.6).
DELETE FROM saga_map_nodes WHERE project_id = sqlc.arg(project_id);

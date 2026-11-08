-- PostgreSQL deletion registries persisted before 0038 name steps of tables that 0038 dropped
-- (writer proposals, canon correction proposals). An unfinished target drops those steps; its
-- later steps take consecutive numbers and restart from pending, since they are idempotent
-- deletes and scrubs. Finished targets keep their recorded history (see 0041).
CREATE TEMPORARY TABLE ck_registry_target (
  tombstone_id uuid NOT NULL, project_id uuid NOT NULL, first_removed integer NOT NULL
) ON COMMIT DROP;

INSERT INTO ck_registry_target (tombstone_id, project_id, first_removed)
SELECT step.tombstone_id, step.project_id, min(step.step_order)
FROM public.deletion_target_step_progress AS step
JOIN public.deletion_target_tasks AS task
  ON task.tombstone_id = step.tombstone_id
 AND task.project_id = step.project_id
 AND task.target = step.target
WHERE step.target = 'postgresql'
  AND task.state <> 'succeeded'
  AND split_part(step.step_name, ':', 2) IN ('writer_proposals', 'canon_correction_proposals')
GROUP BY step.tombstone_id, step.project_id;

DELETE FROM public.deletion_target_step_progress AS step
USING ck_registry_target AS target
WHERE step.tombstone_id = target.tombstone_id
  AND step.project_id = target.project_id
  AND step.target = 'postgresql'
  AND split_part(step.step_name, ':', 2) IN ('writer_proposals', 'canon_correction_proposals');

-- Two passes keep the unique step order free of transient collisions.
UPDATE public.deletion_target_step_progress AS step
   SET step_order = 500 + numbered.position,
       step_name = lpad((500 + numbered.position)::text, 3, '0') || substr(step.step_name, 4),
       state = 'pending', claim_incarnation = NULL, claimed_by_digest = NULL,
       cursor_uuid = NULL, cursor_digest = NULL, processed_count = 0,
       started_at = NULL, updated_at = NULL, completed_at = NULL
  FROM ck_registry_target AS target,
       (
         SELECT remaining.tombstone_id, remaining.project_id, remaining.step_name,
                row_number() OVER (
                  PARTITION BY remaining.tombstone_id, remaining.project_id ORDER BY remaining.step_order
                )::integer AS position
         FROM public.deletion_target_step_progress AS remaining
         WHERE remaining.target = 'postgresql'
       ) AS numbered
 WHERE step.tombstone_id = target.tombstone_id
   AND step.project_id = target.project_id
   AND step.target = 'postgresql'
   AND step.step_order > target.first_removed
   AND numbered.tombstone_id = step.tombstone_id
   AND numbered.project_id = step.project_id
   AND numbered.step_name = step.step_name;

UPDATE public.deletion_target_step_progress AS step
   SET step_order = step.step_order - 500,
       step_name = lpad((step.step_order - 500)::text, 3, '0') || substr(step.step_name, 4)
  FROM ck_registry_target AS target
 WHERE step.tombstone_id = target.tombstone_id
   AND step.project_id = target.project_id
   AND step.target = 'postgresql'
   AND step.step_order > 500;

REVOKE ALL ON TABLE public.deletion_target_step_progress FROM PUBLIC;

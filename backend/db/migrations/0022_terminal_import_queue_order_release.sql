-- Terminal failed queues remain durable history but no longer reserve project order.

DROP INDEX public.import_queues_one_active_per_project;

CREATE UNIQUE INDEX import_queues_one_active_per_project
ON public.import_queues(project_id)
WHERE status IN ('uploading','draft','running');

REVOKE ALL ON TABLE public.import_queues FROM PUBLIC;

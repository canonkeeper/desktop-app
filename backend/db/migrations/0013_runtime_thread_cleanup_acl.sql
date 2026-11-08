-- Allow the product runtime to scrub its durable agent-thread bindings.

REVOKE ALL ON TABLE public.job_agent_threads FROM PUBLIC;
GRANT DELETE ON TABLE public.job_agent_threads TO canonkeeper_product_runtime;

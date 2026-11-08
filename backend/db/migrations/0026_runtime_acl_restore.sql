-- Logical database restores must not depend on provider-preserved ACL metadata.
-- Rebuild the exact runtime-role contracts from migrations 0001-0025.
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM PUBLIC;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM PUBLIC;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM PUBLIC;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA canonkeeper_worker FROM PUBLIC;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA canonkeeper_worker FROM PUBLIC;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA canonkeeper_worker FROM PUBLIC;
REVOKE ALL PRIVILEGES ON SCHEMA canonkeeper_worker FROM PUBLIC;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM canonkeeper_product_runtime;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM canonkeeper_product_runtime;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM canonkeeper_product_runtime;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM canonkeeper_product_runtime;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA canonkeeper_worker FROM canonkeeper_product_runtime;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA canonkeeper_worker FROM canonkeeper_product_runtime;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA canonkeeper_worker FROM canonkeeper_product_runtime;
REVOKE ALL PRIVILEGES ON SCHEMA canonkeeper_worker FROM canonkeeper_product_runtime;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM canonkeeper_governance_owner;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM canonkeeper_governance_owner;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM canonkeeper_governance_owner;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM canonkeeper_governance_owner;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA canonkeeper_worker FROM canonkeeper_governance_owner;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA canonkeeper_worker FROM canonkeeper_governance_owner;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA canonkeeper_worker FROM canonkeeper_governance_owner;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM canonkeeper_worker_discovery;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM canonkeeper_worker_discovery;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM canonkeeper_worker_discovery;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM canonkeeper_worker_discovery;
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA canonkeeper_worker FROM canonkeeper_worker_discovery;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA canonkeeper_worker FROM canonkeeper_worker_discovery;
REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA canonkeeper_worker FROM canonkeeper_worker_discovery;
REVOKE ALL PRIVILEGES ON SCHEMA canonkeeper_worker FROM canonkeeper_worker_discovery;

GRANT USAGE ON SCHEMA public TO canonkeeper_product_runtime;
GRANT USAGE ON SCHEMA public TO canonkeeper_governance_owner;
GRANT USAGE ON SCHEMA canonkeeper_worker TO canonkeeper_worker_discovery;

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE
    public.agent_workflow_inputs,
    public.artifact_source_owners,
    public.artifact_source_refs,
    public.audit_logs,
    public.book_source_members,
    public.books,
    public.canon_author_rows,
    public.canon_book_enrichment_inputs,
    public.canon_correction_proposals,
    public.canon_graph_attempts,
    public.canon_graph_book_slots,
    public.canon_graph_contributions,
    public.canon_graph_draft_review_reads,
    public.canon_graph_draft_rows,
    public.canon_graph_row_evidence,
    public.canon_graph_rows,
    public.chapter_paragraphs,
    public.chapter_source_members,
    public.chapter_summaries,
    public.chapter_text_index_state,
    public.chapter_text_search_rows,
    public.chapters,
    public.chat_sessions,
    public.chat_turns,
    public.deletion_tombstones,
    public.export_artifacts,
    public.import_job_requests,
    public.import_queue_items,
    public.import_queues,
    public.import_uploads,
    public.job_model_bindings,
    public.jobs,
    public.project_invitations,
    public.project_memberships,
    public.project_source_views,
    public.projects,
    public.reader_annotations,
    public.writer_proposals
TO canonkeeper_product_runtime;

GRANT SELECT,INSERT,DELETE ON TABLE
    public.agent_tool_calls,
    public.agent_tool_refs,
    public.canon_graph_overlays
TO canonkeeper_product_runtime;

GRANT SELECT,INSERT,UPDATE ON TABLE
    public.deletion_completion_receipts,
    public.deletion_target_step_progress,
    public.deletion_target_tasks,
    public.idempotency_records,
    public.sessions,
    public.users
TO canonkeeper_product_runtime;

GRANT SELECT,INSERT ON TABLE
    public.deletion_hard_purge_predecessors,
    public.import_queue_audit_verdicts
TO canonkeeper_product_runtime;

REVOKE UPDATE (result_payload) ON TABLE public.agent_tool_calls FROM canonkeeper_product_runtime;
REVOKE UPDATE (ref) ON TABLE public.agent_tool_refs FROM canonkeeper_product_runtime;
GRANT UPDATE (result_payload) ON TABLE public.agent_tool_calls TO canonkeeper_product_runtime;
GRANT UPDATE (ref) ON TABLE public.agent_tool_refs TO canonkeeper_product_runtime;

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.governance_writer_leases
TO canonkeeper_governance_owner;

GRANT SELECT,DELETE,UPDATE ON TABLE
    public.canon_correction_proposals,
    public.writer_proposals
TO canonkeeper_governance_owner;

GRANT SELECT,DELETE ON TABLE
    public.agent_workflow_inputs,
    public.idempotency_records
TO canonkeeper_governance_owner;

GRANT SELECT,UPDATE ON TABLE
    public.audit_logs,
    public.books,
    public.chapters,
    public.chat_sessions,
    public.chat_turns,
    public.deletion_target_tasks,
    public.deletion_tombstones,
    public.export_artifacts,
    public.import_uploads,
    public.jobs,
    public.project_source_views,
    public.projects,
    public.reader_annotations
TO canonkeeper_governance_owner;

GRANT SELECT ON TABLE
    public.artifact_source_owners,
    public.artifact_source_refs,
    public.book_source_members,
    public.canon_author_rows,
    public.canon_graph_book_slots,
    public.canon_graph_contributions,
    public.canon_graph_overlays,
    public.canon_graph_row_evidence,
    public.canon_graph_rows,
    public.chapter_source_members,
    public.chapter_summaries,
    public.chapter_text_index_state,
    public.chapter_text_search_rows,
    public.deletion_completion_receipts,
    public.deletion_hard_purge_predecessors,
    public.import_job_requests,
    public.import_queue_items,
    public.import_queues,
    public.project_memberships
TO canonkeeper_governance_owner;

REVOKE UPDATE (role) ON TABLE public.project_memberships FROM canonkeeper_governance_owner;
GRANT UPDATE (role) ON TABLE public.project_memberships TO canonkeeper_governance_owner;

GRANT EXECUTE ON FUNCTION
    public.ck_artifact_source_owner_is_current(uuid, uuid, text, uuid),
    public.ck_artifact_source_owner_is_lifecycle_readable(uuid, uuid, text, uuid),
    public.ck_artifact_source_owner_is_readable(uuid, uuid, text, uuid),
    public.ck_artifact_source_ref_projection(text, text, uuid, bigint, uuid, bigint, uuid, bigint, text, boolean, uuid, bigint),
    public.ck_artifact_source_ref_projection_v2(text, text, uuid, bigint, uuid, bigint, uuid, bigint, bigint, text, bytea, bytea, text, boolean, uuid, bigint),
    public.ck_artifact_kind_tag(text),
    public.ck_artifact_manifest_digest(text, uuid, uuid, text, bytea[]),
    public.ck_artifact_manifest_projection(text, uuid, uuid, text, bytea[]),
    public.ck_artifact_manifest_digest_v2(text, uuid, uuid, text, bytea[]),
    public.ck_artifact_manifest_projection_v2(text, uuid, uuid, text, bytea[]),
    public.ck_chat_arm(uuid, bigint),
    public.ck_canon_graph_handle_bindings_valid(jsonb),
    public.ck_claim_artifact_source_ref_validation(text, uuid, uuid),
    public.ck_delete_native_indexing_review_coverage(text),
    public.ck_current_indexing_job(uuid, text, uuid, text, uuid, uuid),
    public.ck_deletion_target_receipt_set_digest(text, bytea, bytea, bytea[]),
    public.ck_deletion_target_receipt_set_projection(text, bytea, bytea, bytea[]),
    public.ck_deletion_target_receipt_digest(text, bytea, bytea, text, text, timestamp with time zone),
    public.ck_deletion_target_receipt_projection(text, bytea, bytea, text, text, timestamp with time zone),
    public.ck_expire_governance_writer_lease(bytea, text, bytea),
    public.ck_heartbeat_governance_writer_lease(bytea, text, bytea),
    public.ck_jsonb_object_length(jsonb),
    public.ck_audit_payload_shape_valid(text, text, text, text, jsonb),
    public.ck_jsonb_object_key_count(jsonb),
    public.ck_is_nonzero_digest(bytea),
    public.ck_purge_expired_governance_writer_leases(),
    public.ck_raw_source_vector(text, uuid, bigint, uuid, bigint, uuid, bigint),
    public.ck_register_governance_writer_lease(bytea, text, bytea),
    public.ck_source_generation_digest(uuid, bigint, uuid, bigint, uuid, bigint),
    public.ck_source_generation_projection(uuid, bigint, uuid, bigint, uuid, bigint),
    public.ck_source_hard_purge_contains_derived(public.deletion_tombstones, public.deletion_tombstones),
    public.ck_source_view_arm(text),
    public.ck_tag_byte(integer),
    public.ck_unix_seconds(timestamp with time zone)
TO canonkeeper_product_runtime;

GRANT EXECUTE ON FUNCTION
    public.ck_artifact_source_owner_is_current(uuid, uuid, text, uuid),
    public.ck_artifact_source_owner_is_lifecycle_readable(uuid, uuid, text, uuid),
    public.ck_artifact_source_owner_is_readable(uuid, uuid, text, uuid),
    public.ck_artifact_source_ref_projection(text, text, uuid, bigint, uuid, bigint, uuid, bigint, text, boolean, uuid, bigint),
    public.ck_artifact_source_ref_projection_v2(text, text, uuid, bigint, uuid, bigint, uuid, bigint, bigint, text, bytea, bytea, text, boolean, uuid, bigint),
    public.ck_artifact_kind_tag(text),
    public.ck_artifact_manifest_digest(text, uuid, uuid, text, bytea[]),
    public.ck_artifact_manifest_projection(text, uuid, uuid, text, bytea[]),
    public.ck_artifact_manifest_digest_v2(text, uuid, uuid, text, bytea[]),
    public.ck_artifact_manifest_projection_v2(text, uuid, uuid, text, bytea[]),
    public.ck_chat_arm(uuid, bigint),
    public.ck_claim_artifact_source_ref_validation(text, uuid, uuid),
    public.ck_delete_native_indexing_review_coverage(text),
    public.ck_current_indexing_job(uuid, text, uuid, text, uuid, uuid),
    public.ck_deletion_target_receipt_set_digest(text, bytea, bytea, bytea[]),
    public.ck_deletion_target_receipt_digest(text, bytea, bytea, text, text, timestamp with time zone),
    public.ck_deletion_target_receipt_projection(text, bytea, bytea, text, text, timestamp with time zone),
    public.ck_expire_governance_writer_lease(bytea, text, bytea),
    public.ck_heartbeat_governance_writer_lease(bytea, text, bytea),
    public.ck_jsonb_object_length(jsonb),
    public.ck_is_nonzero_digest(bytea),
    public.ck_purge_expired_governance_writer_leases(),
    public.ck_raw_source_vector(text, uuid, bigint, uuid, bigint, uuid, bigint),
    public.ck_register_governance_writer_lease(bytea, text, bytea),
    public.ck_source_generation_digest(uuid, bigint, uuid, bigint, uuid, bigint),
    public.ck_source_generation_projection(uuid, bigint, uuid, bigint, uuid, bigint),
    public.ck_source_hard_purge_contains_derived(public.deletion_tombstones, public.deletion_tombstones),
    public.ck_source_view_arm(text),
    public.ck_tag_byte(integer),
    public.ck_unix_seconds(timestamp with time zone)
TO canonkeeper_governance_owner;

GRANT EXECUTE ON FUNCTION
    public.ck_enforce_runtime_receipt_mode(),
    public.ck_mark_artifact_source_ref_validation(),
    public.ck_validate_active_artifact_source_owner_currentness(),
    public.ck_validate_canon_graph_contribution_slot(),
    public.ck_validate_canon_graph_overlay_link(),
    public.ck_validate_canon_graph_row_evidence(),
    public.ck_validate_owner_artifact_link(),
    public.ck_validate_owner_ref_set(),
    public.ck_validate_participant_owner_link(),
    public.ck_writer_lease_identity_is_valid(bytea, text, bytea),
    public.governance_writer_fleet_fence_key(),
    public.ck_import_job_claim_candidate(text[]),
    public.ck_canon_book_enrichment_job_claim_candidate(text[]),
    public.ck_simple_job_claim_candidate(text[]),
    public.ck_deletion_target_receipt_projection(text, bytea, bytea, text, text, timestamp with time zone, uuid),
    public.ck_deletion_target_receipt_digest(text, bytea, bytea, text, text, timestamp with time zone, uuid),
    public.ck_deletion_tombstone_projection(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamp with time zone, timestamp with time zone, uuid),
    public.ck_deletion_tombstone_digest(uuid, text, text, text, uuid, bigint, uuid, bigint, uuid, bigint, uuid, bigint, timestamp with time zone, timestamp with time zone, uuid)
TO canonkeeper_governance_owner;

GRANT EXECUTE ON FUNCTION
    canonkeeper_worker.claim_next_deletion_audit_gc_for_worker(),
    canonkeeper_worker.claim_next_deletion_target_task_for_worker(bytea, integer, boolean),
    canonkeeper_worker.claim_next_export_intent_for_worker(),
    canonkeeper_worker.claim_next_import_expiry_for_worker(),
    canonkeeper_worker.claim_next_queued_job_for_worker(text[]),
    canonkeeper_worker.claim_next_source_hard_purge_for_worker(),
    canonkeeper_worker.claim_next_tenant_retention_for_worker()
TO canonkeeper_worker_discovery, canonkeeper_governance_owner;

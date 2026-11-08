package storedb_test

import (
	"context"
	"slices"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

type tablePrivilegeGroup struct {
	privileges []string
	tables     []string
}

type roleTableContract struct {
	role         string
	groups       []tablePrivilegeGroup
	columnExtras []string
}

var productRuntimeTableContract = roleTableContract{
	role: "canonkeeper_product_runtime",
	groups: []tablePrivilegeGroup{
		{
			privileges: []string{"DELETE", "INSERT", "SELECT", "UPDATE"},
			tables: []string{
				"account_tokens", "agent_workflow_inputs", "artifact_source_owners", "artifact_source_refs",
				"book_cover_artifacts", "book_source_members", "books",
				"chapter_cards", "chapter_paragraphs", "chapter_search_texts", "chapter_search_words",
				"chapter_source_members", "chapter_text_index_state", "chapter_text_search_rows", "chapters",
				"chat_sessions", "chat_turns", "deletion_tombstones", "import_job_requests",
				"import_queue_items", "import_queues", "import_uploads", "job_model_bindings", "jobs",
				"personal_notes", "project_invitations", "project_memberships", "project_source_views", "projects",
				"model_limit_samples", "model_usage_days", "writer_quota_overrides", "writer_usage_days",
				"project_illustration_settings", "saga_image_layers", "saga_image_references", "saga_images", "saga_drawings", "saga_names", "saga_map_graphs", "saga_map_nodes", "saga_map_node_parts", "saga_map_names", "saga_map_hidden", "saga_threads", "author_memory", "chapter_drafts", "chapter_draft_notes", "author_materials", "author_material_imports",
				"chapter_draft_packs", "chapter_draft_plans", "chapter_draft_versions", "chapter_draft_originals",
				"illustration_access", "illustration_access_requests", "platform_quota_defaults", "image_upload_consents",
				"promo_permissions", "reading_tests", "reading_test_votes",
				"platform_contacts", "feedback_threads", "feedback_images",
				// The consent to cards and the Obsidian vault of a saga in the desktop build (0114).
				"card_build_permissions", "obsidian_vaults",
			},
		},
		{
			privileges: []string{"INSERT", "SELECT", "UPDATE"},
			tables: []string{
				"deletion_completion_receipts", "deletion_target_step_progress", "deletion_target_tasks",
				"idempotency_records", "sessions", "users",
			},
		},
		{
			privileges: []string{"INSERT", "SELECT"},
			tables:     []string{"chat_turn_attachments", "deletion_hard_purge_predecessors"},
		},
		{
			// The memory of the voice co-author about a draft (0093): written whole, never updated. A message of
			// feedback (0113) and a row of the journal of what went to the AI (0114) are written once too.
			privileges: []string{"DELETE", "INSERT", "SELECT"},
			tables:     []string{"chapter_draft_scenes", "chapter_draft_talks", "feedback_messages", "model_disclosures", "model_requests"},
		},
	},
}

var governanceOwnerTableContract = roleTableContract{
	role: "canonkeeper_governance_owner",
	groups: []tablePrivilegeGroup{
		{
			privileges: []string{"DELETE", "SELECT"},
			tables:     []string{"agent_workflow_inputs", "idempotency_records"},
		},
		{
			// The outcome counter trigger of 0059 (specs/2026-10-28-owner-tools-and-counters.md UC1).
			privileges: []string{"INSERT", "SELECT", "UPDATE"},
			tables:     []string{"writer_usage_days"},
		},
		{
			privileges: []string{"SELECT", "UPDATE"},
			tables: []string{
				"books", "chapters", "chat_sessions", "chat_turns", "deletion_target_tasks",
				"deletion_tombstones", "import_uploads", "jobs", "project_source_views",
				"projects",
			},
		},
		{
			privileges: []string{"SELECT"},
			tables: []string{
				"artifact_source_owners", "artifact_source_refs", "book_source_members",
				"chapter_cards", "chapter_search_texts", "chapter_search_words", "chapter_source_members",
				"chapter_text_index_state", "chapter_text_search_rows", "deletion_completion_receipts",
				"deletion_hard_purge_predecessors", "import_job_requests", "import_queue_items", "import_queues",
				"project_illustration_settings", "project_memberships", "promo_permissions", "saga_image_layers", "saga_image_references", "saga_images", "saga_drawings", "saga_names", "saga_map_graphs", "saga_map_nodes", "saga_map_node_parts", "saga_map_names", "saga_map_hidden", "saga_threads", "author_memory", "chapter_drafts", "chapter_draft_notes", "author_materials", "author_material_imports",
				"chapter_draft_scenes", "chapter_draft_talks", "reading_tests", "reading_test_votes",
				"chapter_draft_packs", "chapter_draft_plans", "chapter_draft_versions", "chapter_draft_originals",
				"chat_turn_attachments",
				"card_build_permissions", "model_requests", "model_disclosures", "obsidian_vaults",
			},
		},
	},
	columnExtras: []string{"project_memberships|role|UPDATE", "users|disabled_at|SELECT", "users|id|SELECT"},
}

var governanceRuntimeFunctions = []string{
	"ck_artifact_source_owner_is_current", "ck_artifact_source_owner_is_lifecycle_readable",
	"ck_artifact_source_owner_is_readable", "ck_artifact_source_ref_projection",
	"ck_artifact_source_ref_projection_v2", "ck_artifact_kind_tag",
	"ck_artifact_manifest_digest", "ck_artifact_manifest_projection",
	"ck_artifact_manifest_digest_v2", "ck_artifact_manifest_projection_v2",
	"ck_chat_arm", "ck_claim_artifact_source_ref_validation",
	"ck_deletion_target_receipt_set_digest", "ck_deletion_target_receipt_digest",
	"ck_deletion_target_receipt_projection", "ck_jsonb_object_length",
	"ck_is_nonzero_digest",
	"ck_raw_source_vector",
	"ck_source_generation_digest", "ck_source_generation_projection",
	"ck_source_hard_purge_contains_derived", "ck_source_view_arm",
	"ck_tag_byte", "ck_unix_seconds",
}

var governanceDeniedFunctions = []string{"ck_deletion_target_receipt_set_projection"}

func TestRuntimeTablePrivilegeContractsAreClosed(t *testing.T) {
	db := testutil.OpenIsolatedPostgresMigrationDB(t, "ck_runtime_acl_contract")
	schema := testutil.IsolatedPostgresSchema(t, db)
	contracts := []roleTableContract{
		productRuntimeTableContract,
		governanceOwnerTableContract,
		{role: "canonkeeper_worker_discovery"},
		{role: "PUBLIC"},
	}
	for _, contract := range contracts {
		got := readTablePrivileges(t, db.Pool, schema, contract.role)
		want := expectedTablePrivileges(contract)
		if !slices.Equal(got, want) {
			t.Fatalf("table privileges for %s = %v, want %v", contract.role, got, want)
		}
		extras := readColumnPrivilegeExtras(t, db.Pool, schema, contract)
		if !slices.Equal(extras, contract.columnExtras) {
			t.Fatalf("column extras for %s = %v, want %v", contract.role, extras, contract.columnExtras)
		}
	}

	for _, check := range []struct {
		role, schemaName string
		want             bool
	}{
		{"canonkeeper_product_runtime", schema, true},
		{"canonkeeper_product_runtime", schema + "_worker", false},
		{"canonkeeper_governance_owner", schema, true},
		{"canonkeeper_governance_owner", schema + "_worker", true},
		{"canonkeeper_worker_discovery", schema, false},
		{"canonkeeper_worker_discovery", schema + "_worker", true},
	} {
		var got bool
		if err := db.Pool.QueryRow(context.Background(), "SELECT has_schema_privilege($1, $2, 'USAGE')",
			check.role, check.schemaName).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("schema privilege %s/%s = %t, want %t", check.role, check.schemaName, got, check.want)
		}
	}

	assertRoleFunctionNames(t, db.Pool, schema, "canonkeeper_product_runtime", productRuntimeFunctions, true)
	assertRoleFunctionNames(t, db.Pool, schema, "canonkeeper_governance_owner", governanceRuntimeFunctions, true)
	assertRoleFunctionNames(t, db.Pool, schema, "canonkeeper_governance_owner", governanceDeniedFunctions, false)
	assertRoleFunctionNames(t, db.Pool, schema, "canonkeeper_worker_discovery", productRuntimeFunctions, false)
	assertRoleFunctionNames(t, db.Pool, schema+"_worker", "canonkeeper_worker_discovery", workerDiscoveryFunctions, true)
	assertRoleFunctionNames(t, db.Pool, schema+"_worker", "canonkeeper_governance_owner", workerDiscoveryFunctions, true)
	assertRoleFunctionNames(t, db.Pool, schema+"_worker", "canonkeeper_product_runtime", workerDiscoveryFunctions, false)
}

func TestRuntimeACLRepairRemovesRestoredPublicDrift(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_restore_public_acl", 25)
	schema := testutil.IsolatedPostgresSchema(t, db)
	quotedSchema := quoteIdentifier(schema)
	sequence := quotedSchema + ".restore_acl_probe_seq"
	for _, statement := range []string{
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO PUBLIC",
		"GRANT SELECT ON TABLE " + quotedSchema + ".projects TO PUBLIC",
		"GRANT EXECUTE ON FUNCTION " + quotedSchema + ".ck_jsonb_object_length(jsonb) TO PUBLIC",
		"CREATE SEQUENCE " + sequence,
		"GRANT USAGE ON SEQUENCE " + sequence + " TO PUBLIC",
		"ALTER FUNCTION " + quotedSchema + ".ck_writer_lease_identity_is_valid(bytea,text,bytea) OWNER TO CURRENT_USER",
		"REVOKE ALL ON FUNCTION " + quotedSchema + ".ck_writer_lease_identity_is_valid(bytea,text,bytea) FROM canonkeeper_governance_owner",
		"ALTER FUNCTION " + quotedSchema + ".governance_writer_fleet_fence_key() OWNER TO CURRENT_USER",
		"REVOKE ALL ON FUNCTION " + quotedSchema + ".governance_writer_fleet_fence_key() FROM canonkeeper_governance_owner",
		"ALTER FUNCTION " + quoteIdentifier(schema+"_worker") + ".claim_next_queued_job_for_worker(text[]) OWNER TO CURRENT_USER",
		"REVOKE ALL ON FUNCTION " + quoteIdentifier(schema+"_worker") + ".claim_next_queued_job_for_worker(text[]) FROM canonkeeper_governance_owner",
	} {
		if _, err := db.Pool.Exec(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := testutil.ApplyNextMigration(t, db, 25); err != nil {
		t.Fatal(err)
	}
	assertSchemaPublicPrivilege(t, db.Pool, schema, "USAGE", false)
	assertTablePublicPrivilege(t, db.Pool, quotedSchema+".projects", "SELECT", false)
	assertFunctionPublicPrivilege(t, db.Pool, quotedSchema+".ck_jsonb_object_length(jsonb)", false)
	var publicSequenceGrants int
	if err := db.Pool.QueryRow(context.Background(), `
SELECT count(*)
FROM information_schema.usage_privileges
WHERE object_schema=$1 AND object_name='restore_acl_probe_seq' AND grantee='PUBLIC'
`, schema).Scan(&publicSequenceGrants); err != nil {
		t.Fatal(err)
	}
	if publicSequenceGrants != 0 {
		t.Fatalf("restored PUBLIC sequence grants = %d, want 0", publicSequenceGrants)
	}
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_governance_owner", quotedSchema+".ck_writer_lease_identity_is_valid(bytea,text,bytea)", true)
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_governance_owner", quotedSchema+".governance_writer_fleet_fence_key()", true)
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_governance_owner", quoteIdentifier(schema+"_worker")+".claim_next_queued_job_for_worker(text[])", true)
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_worker_discovery", quoteIdentifier(schema+"_worker")+".claim_next_queued_job_for_worker(text[])", true)
	for _, role := range []string{
		"canonkeeper_product_runtime", "canonkeeper_governance_owner", "canonkeeper_worker_discovery",
	} {
		var grants int
		if err := db.Pool.QueryRow(context.Background(), `
SELECT count(*)
FROM information_schema.usage_privileges
WHERE object_schema=$1 AND object_name='restore_acl_probe_seq' AND grantee=$2
`, schema, role).Scan(&grants); err != nil {
			t.Fatal(err)
		}
		if grants != 0 {
			t.Fatalf("unexpected sequence grants for %s = %d", role, grants)
		}
	}
}

func expectedTablePrivileges(contract roleTableContract) []string {
	var result []string
	for _, group := range contract.groups {
		for _, table := range group.tables {
			for _, privilege := range group.privileges {
				result = append(result, table+"|"+privilege)
			}
		}
	}
	slices.Sort(result)
	return result
}

func readTablePrivileges(t *testing.T, db migrationACLQueryer, schema, role string) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), `
SELECT table_name, privilege_type
FROM information_schema.table_privileges
WHERE table_schema=$1 AND grantee=$2
ORDER BY table_name, privilege_type`, schema, role)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var table, privilege string
		if err := rows.Scan(&table, &privilege); err != nil {
			t.Fatal(err)
		}
		result = append(result, table+"|"+privilege)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// The database orders by its collation; the contract is sorted bytewise.
	slices.Sort(result)
	return result
}

func readColumnPrivilegeExtras(t *testing.T, db migrationACLQueryer, schema string, contract roleTableContract) []string {
	t.Helper()
	allowed := make(map[string]map[string]bool)
	for _, group := range contract.groups {
		for _, table := range group.tables {
			allowed[table] = make(map[string]bool)
			for _, privilege := range group.privileges {
				allowed[table][privilege] = true
			}
		}
	}
	rows, err := db.Query(context.Background(), `
SELECT table_name, column_name, privilege_type
FROM information_schema.column_privileges
WHERE table_schema=$1 AND grantee=$2
ORDER BY table_name, column_name, privilege_type`, schema, contract.role)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var table, column, privilege string
		if err := rows.Scan(&table, &column, &privilege); err != nil {
			t.Fatal(err)
		}
		if !allowed[table][privilege] {
			result = append(result, table+"|"+column+"|"+privilege)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertRoleFunctionNames(
	t *testing.T,
	db migrationQueryer,
	schema, role string,
	functions []string,
	want bool,
) {
	t.Helper()
	var count int
	if err := db.QueryRow(context.Background(), `
SELECT count(DISTINCT procedure.proname)
FROM pg_catalog.pg_proc AS procedure
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid=procedure.pronamespace
WHERE namespace.nspname=$1
  AND procedure.proname=ANY($2::text[])
  AND has_function_privilege($3, procedure.oid, 'EXECUTE')=$4
`, schema, functions, role, want).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(functions) {
		t.Fatalf("function privilege count %s/%s/%t = %d, want %d", role, schema, want, count, len(functions))
	}
}

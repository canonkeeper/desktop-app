package storedb_test

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	dbmigrations "canonkeeper/backend/db/migrations"
	"canonkeeper/backend/internal/testutil"
	"github.com/jackc/pgx/v5"
)

type migrationQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type migrationACLQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

var publicGrant = regexp.MustCompile(`(?is)\bTO\s+PUBLIC\b`)
var catalogWideFunctionGrant = regexp.MustCompile(`(?is)^[ \t]*GRANT\s+[^;]*\s+ON\s+ALL\s+FUNCTIONS\s+IN\s+SCHEMA\b`)

var productRuntimeFunctions = []string{
	"ck_artifact_source_owner_is_current", "ck_artifact_source_owner_is_lifecycle_readable",
	"ck_artifact_source_owner_is_readable", "ck_artifact_source_ref_projection",
	"ck_artifact_source_ref_projection_v2", "ck_artifact_kind_tag",
	"ck_artifact_manifest_digest", "ck_artifact_manifest_projection",
	"ck_artifact_manifest_digest_v2", "ck_artifact_manifest_projection_v2",
	"ck_chat_arm", "ck_claim_artifact_source_ref_validation",
	"ck_deletion_predecessor_set_digest", "ck_deletion_predecessor_set_projection",
	"ck_deletion_selected_vector_digest", "ck_deletion_selected_vector_projection",
	"ck_deletion_target_receipt_set_digest", "ck_deletion_target_receipt_set_projection", "ck_deletion_target_receipt_digest",
	"ck_deletion_target_receipt_projection", "ck_deletion_tombstone_digest", "ck_deletion_tombstone_projection",
	"ck_jsonb_object_length", "ck_anonymize_writer_usage", "ck_owner_days",
	"ck_is_nonzero_digest",
	"ck_raw_source_vector",
	"ck_source_generation_digest", "ck_source_generation_projection",
	"ck_source_hard_purge_contains_derived", "ck_source_hard_purge_predecessor_set_is_valid",
	"ck_source_view_arm",
	"ck_tag_byte", "ck_unix_micros", "ck_unix_seconds",
}

var productRuntimeSourceHardPurgeFunctions = []string{
	"ck_source_hard_purge_predecessor_set_is_valid(uuid,uuid)",
	"ck_deletion_selected_vector_digest(text,uuid,bigint,uuid,bigint,uuid,bigint,uuid,bigint)",
	"ck_deletion_selected_vector_projection(text,uuid,bigint,uuid,bigint,uuid,bigint,uuid,bigint)",
	"ck_deletion_predecessor_set_digest(bytea,uuid[],bytea[],bytea[])",
	"ck_deletion_predecessor_set_projection(bytea,uuid[],bytea[],bytea[])",
	"ck_deletion_tombstone_digest(uuid,text,text,text,uuid,bigint,uuid,bigint,uuid,bigint,uuid,bigint,timestamp with time zone,timestamp with time zone,uuid)",
	"ck_deletion_tombstone_projection(uuid,text,text,text,uuid,bigint,uuid,bigint,uuid,bigint,uuid,bigint,timestamp with time zone,timestamp with time zone,uuid)",
	"ck_deletion_tombstone_projection(uuid,text,text,text,uuid,bigint,uuid,bigint,uuid,bigint,uuid,bigint,timestamp with time zone,timestamp with time zone)",
	"ck_unix_micros(timestamp with time zone)",
}

var workerDiscoveryFunctions = []string{
	"claim_next_deletion_audit_gc_for_worker", "claim_next_deletion_target_task_for_worker",
	"claim_next_import_expiry_for_worker", "claim_next_queued_job_for_worker",
	"claim_next_source_hard_purge_for_worker",
}

func TestMigrationObjectsHaveExactRuntimePrivileges(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range inventory {
		sql := strings.ToUpper(string(migration.SQL))
		if publicGrant.MatchString(sql) {
			t.Fatalf("migration %s grants an object to PUBLIC", migration.Name)
		}
		if catalogWideFunctionGrant.MatchString(string(migration.SQL)) {
			t.Fatalf("migration %s grants catalog-wide function access", migration.Name)
		}
		if !strings.Contains(sql, "REVOKE") {
			t.Fatalf("migration %s has no explicit privilege revocation", migration.Name)
		}
	}

	db := testutil.OpenIsolatedPostgresMigrationDB(t, "ck_migration_acl")
	schema := testutil.IsolatedPostgresSchema(t, db)
	violations, err := findMigrationACLViolations(context.Background(), db.Pool, schema, schema+"_worker")
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("migration ACL violations:\n%s", strings.Join(violations, "\n"))
	}
	functionViolations, err := findUnexpectedFunctionGrants(context.Background(), db.Pool, schema, schema+"_worker")
	if err != nil {
		t.Fatal(err)
	}
	if len(functionViolations) != 0 {
		t.Fatalf("migration function ACL violations:\n%s", strings.Join(functionViolations, "\n"))
	}
	schemaViolations, err := findUnexpectedSchemaGrants(context.Background(), db.Pool, schema, schema+"_worker")
	if err != nil {
		t.Fatal(err)
	}
	if len(schemaViolations) != 0 {
		t.Fatalf("migration schema ACL violations:\n%s", strings.Join(schemaViolations, "\n"))
	}

	assertSchemaPrivilege(t, db.Pool, "canonkeeper_product_runtime", schema, "USAGE", true)
	assertSchemaPrivilege(t, db.Pool, "canonkeeper_worker_discovery", schema, "USAGE", false)
	assertSchemaPublicPrivilege(t, db.Pool, schema, "USAGE", false)

	projectSourceViews := fmt.Sprintf("%s.project_source_views", quoteIdentifier(schema))
	assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", projectSourceViews, "SELECT", true)
	assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", projectSourceViews, "SELECT", false)
	assertTablePublicPrivilege(t, db.Pool, projectSourceViews, "SELECT", false)

	chapterCards := fmt.Sprintf("%s.chapter_cards", quoteIdentifier(schema))
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", chapterCards, privilege, true)
		assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", chapterCards, privilege, false)
		assertTablePublicPrivilege(t, db.Pool, chapterCards, privilege, false)
	}
	sagaNames := fmt.Sprintf("%s.saga_names", quoteIdentifier(schema))
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", sagaNames, privilege, true)
		assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", sagaNames, privilege, false)
		assertTablePublicPrivilege(t, db.Pool, sagaNames, privilege, false)
	}
	assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", sagaNames, "SELECT", true)
	for _, table := range []string{"saga_map_graphs", "saga_map_nodes", "saga_map_node_parts", "saga_map_names"} {
		sagaMap := fmt.Sprintf("%s.%s", quoteIdentifier(schema), table)
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", sagaMap, privilege, true)
			assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", sagaMap, privilege, false)
			assertTablePublicPrivilege(t, db.Pool, sagaMap, privilege, false)
		}
		assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", sagaMap, "SELECT", true)
	}
	sagaThreads := fmt.Sprintf("%s.saga_threads", quoteIdentifier(schema))
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", sagaThreads, privilege, true)
		assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", sagaThreads, privilege, false)
		assertTablePublicPrivilege(t, db.Pool, sagaThreads, privilege, false)
	}
	assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", sagaThreads, "SELECT", true)
	authorMemory := fmt.Sprintf("%s.author_memory", quoteIdentifier(schema))
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", authorMemory, privilege, true)
		assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", authorMemory, privilege, false)
		assertTablePublicPrivilege(t, db.Pool, authorMemory, privilege, false)
	}
	assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", authorMemory, "SELECT", true)
	for _, table := range []string{"author_materials", "author_material_imports"} {
		materials := fmt.Sprintf("%s.%s", quoteIdentifier(schema), table)
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", materials, privilege, true)
			assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", materials, privilege, false)
			assertTablePublicPrivilege(t, db.Pool, materials, privilege, false)
		}
		assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", materials, "SELECT", true)
	}
	mapHidden := fmt.Sprintf("%s.saga_map_hidden", quoteIdentifier(schema))
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", mapHidden, privilege, true)
		assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", mapHidden, privilege, false)
		assertTablePublicPrivilege(t, db.Pool, mapHidden, privilege, false)
	}
	assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", mapHidden, "SELECT", true)
	readyWaves := fmt.Sprintf("%s.saga_ready_waves", quoteIdentifier(schema))
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", readyWaves, privilege, true)
		assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", readyWaves, privilege, false)
		assertTablePublicPrivilege(t, db.Pool, readyWaves, privilege, false)
	}
	assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", readyWaves, "SELECT", true)
	// The snapshot of the last update of a book from a file (0142, specs/2026-11-12-ux10-book-revert.md BR1).
	for _, table := range []string{"book_update_snapshots", "book_update_snapshot_chapters"} {
		snapshot := fmt.Sprintf("%s.%s", quoteIdentifier(schema), table)
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", snapshot, privilege, true)
			assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", snapshot, privilege, false)
			assertTablePublicPrivilege(t, db.Pool, snapshot, privilege, false)
		}
		assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", snapshot, "SELECT", true)
	}
	// The codes of the link of the app and the linked apps (0143, specs/2026-11-13-desk-link.md): the runtime of the site
	// alone; nobody else reads a hash of a code or of a pass.
	// The permits of a linked app and the corrections under them (0144, specs/2026-11-13-desk-permits.md): the runtime of
	// the site alone, as the apps they belong to.
	for _, table := range []string{"app_link_codes", "app_devices", "model_permits", "model_permit_units"} {
		linked := fmt.Sprintf("%s.%s", quoteIdentifier(schema), table)
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", linked, privilege, true)
			assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", linked, privilege, false)
			assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", linked, privilege, false)
			assertTablePublicPrivilege(t, db.Pool, linked, privilege, false)
		}
	}
	// The marks of the letters by time and the author's switch of the letters of his tier (0126): the runtime reads and
	// writes them, nobody else (specs/2026-11-10-service-letters.md M3.2, M4.4, M4.5).
	for table, columns := range map[string][]string{
		"users":                {"verification_reminder_at", "tier_letters", "last_seen_at"},
		"subscription_periods": {"ending_letter_at", "ended_letter_at"},
	} {
		qualified := fmt.Sprintf("%s.%s", quoteIdentifier(schema), table)
		for _, column := range columns {
			for _, privilege := range []string{"SELECT", "UPDATE"} {
				assertColumnPrivilege(t, db.Pool, "canonkeeper_product_runtime", qualified, column, privilege, true)
				assertColumnPrivilege(t, db.Pool, "canonkeeper_worker_discovery", qualified, column, privilege, false)
				assertColumnPrivilege(t, db.Pool, "canonkeeper_governance_owner", qualified, column, privilege, false)
			}
		}
	}
	for _, table := range []string{"reading_tests", "reading_test_votes"} {
		reading := fmt.Sprintf("%s.%s", quoteIdentifier(schema), table)
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", reading, privilege, true)
			assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", reading, privilege, false)
			assertTablePublicPrivilege(t, db.Pool, reading, privilege, false)
		}
		assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", reading, "SELECT", true)
	}
	chapterDrafts := fmt.Sprintf("%s.chapter_drafts", quoteIdentifier(schema))
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", chapterDrafts, privilege, true)
		assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", chapterDrafts, privilege, false)
		assertTablePublicPrivilege(t, db.Pool, chapterDrafts, privilege, false)
	}
	assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", chapterDrafts, "SELECT", true)
	draftNotes := fmt.Sprintf("%s.chapter_draft_notes", quoteIdentifier(schema))
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", draftNotes, privilege, true)
		assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", draftNotes, privilege, false)
		assertTablePublicPrivilege(t, db.Pool, draftNotes, privilege, false)
	}
	assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", draftNotes, "SELECT", true)
	// The plan, the packs and the versions of the text of the writing agent (0107), and the chapter the author loaded
	// as a file (0111), go as the draft goes.
	for _, table := range []string{"chapter_draft_packs", "chapter_draft_plans", "chapter_draft_versions", "chapter_draft_originals"} {
		agentData := fmt.Sprintf("%s.%s", quoteIdentifier(schema), table)
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", agentData, privilege, true)
			assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", agentData, privilege, false)
			assertTablePublicPrivilege(t, db.Pool, agentData, privilege, false)
		}
		assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", agentData, "SELECT", true)
	}
	for _, table := range []string{"chapter_draft_talks", "chapter_draft_scenes"} {
		voiceMemory := fmt.Sprintf("%s.%s", quoteIdentifier(schema), table)
		for _, privilege := range []string{"SELECT", "INSERT", "DELETE"} {
			assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", voiceMemory, privilege, true)
			assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", voiceMemory, privilege, false)
			assertTablePublicPrivilege(t, db.Pool, voiceMemory, privilege, false)
		}
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", voiceMemory, "UPDATE", false)
		assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", voiceMemory, "SELECT", true)
	}
	// The chapter attached to a question of the chat (0112) is written once and goes with its turn.
	attachments := fmt.Sprintf("%s.chat_turn_attachments", quoteIdentifier(schema))
	for _, privilege := range []string{"SELECT", "INSERT"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", attachments, privilege, true)
	}
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", attachments, privilege, false)
		assertTablePublicPrivilege(t, db.Pool, attachments, privilege, false)
	}
	for _, privilege := range []string{"UPDATE", "DELETE"} {
		assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", attachments, privilege, false)
	}
	assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", attachments, "SELECT", true)
	// The desktop build (0114): the consent to cards and the Obsidian vault of a saga change; a row of the journal of
	// what went to the AI is written once and goes when the author clears the journal or the saga is purged.
	for _, table := range []string{"card_build_permissions", "obsidian_vaults", "model_requests", "model_disclosures"} {
		desktop := fmt.Sprintf("%s.%s", quoteIdentifier(schema), table)
		journal := table == "model_requests" || table == "model_disclosures"
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", desktop, privilege, !journal || privilege != "UPDATE")
			assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", desktop, privilege, false)
			assertTablePublicPrivilege(t, db.Pool, desktop, privilege, false)
		}
		assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", desktop, "SELECT", true)
	}
	idempotencyRecords := fmt.Sprintf("%s.idempotency_records", quoteIdentifier(schema))
	assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", idempotencyRecords, "DELETE", false)
	assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", idempotencyRecords, "SELECT", true)
	assertTablePrivilege(t, db.Pool, "canonkeeper_governance_owner", idempotencyRecords, "DELETE", true)

	ledger := fmt.Sprintf("%s.schema_migrations", quoteIdentifier(schema))
	assertTablePrivilege(t, db.Pool, "canonkeeper_product_runtime", ledger, "SELECT", false)
	assertTablePrivilege(t, db.Pool, "canonkeeper_worker_discovery", ledger, "SELECT", false)
	assertTablePublicPrivilege(t, db.Pool, ledger, "SELECT", false)

	function := fmt.Sprintf("%s.claim_next_deletion_target_task_for_worker(bytea,integer,boolean)", quoteIdentifier(schema+"_worker"))
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_worker_discovery", function, true)
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_product_runtime", function, false)
	assertFunctionPublicPrivilege(t, db.Pool, function, false)

	productFunction := fmt.Sprintf("%s.ck_source_generation_digest(uuid,bigint,uuid,bigint,uuid,bigint)", quoteIdentifier(schema))
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_product_runtime", productFunction, true)
	for _, signature := range productRuntimeSourceHardPurgeFunctions {
		function := fmt.Sprintf("%s.%s", quoteIdentifier(schema), signature)
		assertFunctionPrivilege(t, db.Pool, "canonkeeper_product_runtime", function, true)
		assertFunctionPrivilege(t, db.Pool, "canonkeeper_worker_discovery", function, false)
		assertFunctionPublicPrivilege(t, db.Pool, function, false)
	}
	predecessorValidator := fmt.Sprintf("%s.%s", quoteIdentifier(schema), productRuntimeSourceHardPurgeFunctions[0])
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_governance_owner", predecessorValidator, false)
	productDeniedFunction := fmt.Sprintf("%s.ck_deletion_tombstone_digest(uuid,text,text,text,uuid,bigint,uuid,bigint,uuid,bigint,uuid,bigint,timestamp with time zone,timestamp with time zone)", quoteIdentifier(schema))
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_product_runtime", productDeniedFunction, false)
	deletionProjectionFunction := fmt.Sprintf("%s.ck_deletion_target_receipt_set_projection(text,bytea,bytea,bytea[])", quoteIdentifier(schema))
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_product_runtime", deletionProjectionFunction, true)
	assertFunctionPrivilege(t, db.Pool, "canonkeeper_worker_discovery", deletionProjectionFunction, false)
	assertFunctionPublicPrivilege(t, db.Pool, deletionProjectionFunction, false)
}

func TestMigrationACLCheckRejectsDefaultPublicFunction(t *testing.T) {
	db := testutil.OpenIsolatedPostgresMigrationDB(t, "ck_migration_acl_probe")
	schema := testutil.IsolatedPostgresSchema(t, db)
	if _, err := db.Pool.Exec(context.Background(), fmt.Sprintf(
		`CREATE FUNCTION %s.rogue_public() RETURNS integer LANGUAGE sql AS 'SELECT 1'`,
		quoteIdentifier(schema),
	)); err != nil {
		t.Fatal(err)
	}

	violations, err := findMigrationACLViolations(context.Background(), db.Pool, schema, schema+"_worker")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(violations, func(value string) bool {
		return strings.Contains(value, "rogue_public") && strings.Contains(value, "PUBLIC")
	}) {
		t.Fatalf("violations = %v, want rogue_public PUBLIC access", violations)
	}
}

func TestMigrationACLCheckRejectsKnownRoleOutsideFunctionContract(t *testing.T) {
	db := testutil.OpenIsolatedPostgresMigrationDB(t, "ck_migration_acl_probe")
	productSchema := testutil.IsolatedPostgresSchema(t, db)
	workerSchema := productSchema + "_worker"
	rogueSchema := workerSchema + "_rogue"
	if _, err := db.Pool.Exec(context.Background(), "CREATE SCHEMA "+quoteIdentifier(rogueSchema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), "DROP SCHEMA "+quoteIdentifier(rogueSchema)+" CASCADE")
	})
	for _, schema := range []string{productSchema, workerSchema} {
		if _, err := db.Pool.Exec(context.Background(), fmt.Sprintf(
			`CREATE FUNCTION %s.rogue_known_role() RETURNS integer LANGUAGE sql AS 'SELECT 1'`,
			quoteIdentifier(schema),
		)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(context.Background(), fmt.Sprintf(
			`GRANT EXECUTE ON FUNCTION %s.rogue_known_role() TO canonkeeper_product_runtime`,
			quoteIdentifier(schema),
		)); err != nil {
			t.Fatal(err)
		}
	}

	violations, err := findUnexpectedFunctionGrants(context.Background(), db.Pool, productSchema, workerSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{productSchema, workerSchema} {
		if !slices.ContainsFunc(violations, func(value string) bool {
			return strings.Contains(value, schema+".rogue_known_role") &&
				strings.Contains(value, "canonkeeper_product_runtime EXECUTE")
		}) {
			t.Fatalf("violations = %v, want rogue_known_role rejection in %s", violations, schema)
		}
	}

	if _, err := db.Pool.Exec(context.Background(), fmt.Sprintf(
		`GRANT USAGE ON SCHEMA %s TO canonkeeper_product_runtime`,
		quoteIdentifier(rogueSchema),
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), fmt.Sprintf(
		`GRANT CREATE ON SCHEMA %s TO canonkeeper_worker_discovery`,
		quoteIdentifier(productSchema),
	)); err != nil {
		t.Fatal(err)
	}
	schemaViolations, err := findUnexpectedSchemaGrants(context.Background(), db.Pool, productSchema, workerSchema, rogueSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(schemaViolations, func(value string) bool {
		return strings.Contains(value, rogueSchema) &&
			strings.Contains(value, "canonkeeper_product_runtime USAGE")
	}) || !slices.ContainsFunc(schemaViolations, func(value string) bool {
		return strings.Contains(value, productSchema) &&
			strings.Contains(value, "canonkeeper_worker_discovery CREATE")
	}) {
		t.Fatalf("schema violations = %v, want wrong-schema and wrong-privilege rejections", schemaViolations)
	}
}

func findMigrationACLViolations(ctx context.Context, db migrationACLQueryer, schemas ...string) ([]string, error) {
	const query = `
WITH migration_objects AS (
  SELECT
    'schema'::text AS object_kind,
    namespace.nspname AS schema_name,
    namespace.nspname AS object_name,
    namespace.nspowner AS owner_oid,
    namespace.nspacl AS acl
  FROM pg_catalog.pg_namespace AS namespace
  WHERE namespace.nspname = ANY($1::text[])

  UNION ALL

  SELECT
    'relation'::text,
    namespace.nspname,
    relation.relname,
    relation.relowner,
    relation.relacl
  FROM pg_catalog.pg_class AS relation
  JOIN pg_catalog.pg_namespace AS namespace
    ON namespace.oid = relation.relnamespace
  WHERE namespace.nspname = ANY($1::text[])
    AND relation.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')

  UNION ALL

  SELECT
    'function'::text,
    namespace.nspname,
    procedure.oid::regprocedure::text,
    procedure.proowner,
    procedure.proacl
  FROM pg_catalog.pg_proc AS procedure
  JOIN pg_catalog.pg_namespace AS namespace
    ON namespace.oid = procedure.pronamespace
  WHERE namespace.nspname = ANY($1::text[])
),
violations AS (
  SELECT
    object_kind,
    schema_name,
    object_name,
    CASE
      WHEN object_kind = 'function' THEN 'implicit ACL exposes default PUBLIC privilege'
      ELSE 'implicit ACL'
    END AS reason
  FROM migration_objects
  WHERE acl IS NULL

  UNION ALL

  SELECT
    object.object_kind,
    object.schema_name,
    object.object_name,
    CASE
      WHEN privilege.grantee = 0 THEN 'PUBLIC ' || privilege.privilege_type
      ELSE 'unexpected grantee ' || COALESCE(grantee.rolname, privilege.grantee::text) || ' ' || privilege.privilege_type
    END
  FROM migration_objects AS object
  CROSS JOIN LATERAL pg_catalog.aclexplode(object.acl) AS privilege
  LEFT JOIN pg_catalog.pg_roles AS grantee
    ON grantee.oid = privilege.grantee
  WHERE privilege.grantee = 0
    OR (
      privilege.grantee <> object.owner_oid
      AND COALESCE(grantee.rolname, '') <> current_user
      AND COALESCE(grantee.rolname, '') NOT IN (
		'canonkeeper_governance_owner',
        'canonkeeper_product_runtime',
        'canonkeeper_worker_discovery'
      )
    )
)
SELECT object_kind, schema_name, object_name, reason
FROM violations
ORDER BY object_kind, schema_name, object_name, reason`
	rows, err := db.Query(ctx, query, schemas)
	if err != nil {
		return nil, fmt.Errorf("inspect migration ACLs: %w", err)
	}
	defer rows.Close()
	var violations []string
	for rows.Next() {
		var kind, schema, object, reason string
		if err := rows.Scan(&kind, &schema, &object, &reason); err != nil {
			return nil, fmt.Errorf("scan migration ACL violation: %w", err)
		}
		violations = append(violations, fmt.Sprintf("%s %s.%s: %s", kind, schema, object, reason))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect migration ACL rows: %w", err)
	}
	return violations, nil
}

func findUnexpectedFunctionGrants(ctx context.Context, db migrationACLQueryer, productSchema, workerSchema string) ([]string, error) {
	const query = `
SELECT namespace.nspname, procedure.proname, COALESCE(grantee.rolname, privilege.grantee::text), privilege.privilege_type
FROM pg_catalog.pg_proc AS procedure
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
CROSS JOIN LATERAL pg_catalog.aclexplode(
  COALESCE(procedure.proacl, pg_catalog.acldefault('f', procedure.proowner))
) AS privilege
LEFT JOIN pg_catalog.pg_roles AS grantee ON grantee.oid = privilege.grantee
WHERE namespace.nspname = ANY($1::text[])
  AND privilege.grantee <> procedure.proowner
  AND NOT (
    (
      namespace.nspname = $2
      AND procedure.proname = ANY($4::text[])
      AND grantee.rolname = 'canonkeeper_product_runtime'
      AND privilege.privilege_type = 'EXECUTE'
    ) OR (
      namespace.nspname = $3
      AND procedure.proname = ANY($5::text[])
      AND grantee.rolname = 'canonkeeper_worker_discovery'
      AND privilege.privilege_type = 'EXECUTE'
    ) OR (
      namespace.nspname = $2
      AND procedure.proname = ANY($4::text[])
      AND grantee.rolname = 'canonkeeper_governance_owner'
      AND privilege.privilege_type = 'EXECUTE'
    ) OR (
      (
        (namespace.nspname = $2 AND procedure.proname = ANY($4::text[]))
        OR (namespace.nspname = $3 AND procedure.proname = ANY($5::text[]))
      )
      AND grantee.rolname = current_user
      AND privilege.privilege_type = 'EXECUTE'
    )
  )
ORDER BY namespace.nspname, procedure.proname, grantee.rolname, privilege.privilege_type`
	rows, err := db.Query(ctx, query, []string{productSchema, workerSchema}, productSchema, workerSchema, productRuntimeFunctions, workerDiscoveryFunctions)
	if err != nil {
		return nil, fmt.Errorf("inspect function ACLs: %w", err)
	}
	defer rows.Close()
	var violations []string
	for rows.Next() {
		var schema, object, grantee, privilege string
		if err := rows.Scan(&schema, &object, &grantee, &privilege); err != nil {
			return nil, fmt.Errorf("scan function ACL violation: %w", err)
		}
		violations = append(violations, fmt.Sprintf("function %s.%s: %s %s", schema, object, grantee, privilege))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect function ACL rows: %w", err)
	}
	return violations, nil
}

func findUnexpectedSchemaGrants(ctx context.Context, db migrationACLQueryer, productSchema, workerSchema string, extraSchemas ...string) ([]string, error) {
	const query = `
SELECT namespace.nspname, COALESCE(grantee.rolname, privilege.grantee::text), privilege.privilege_type
FROM pg_catalog.pg_namespace AS namespace
CROSS JOIN LATERAL pg_catalog.aclexplode(
  COALESCE(namespace.nspacl, pg_catalog.acldefault('n', namespace.nspowner))
) AS privilege
LEFT JOIN pg_catalog.pg_roles AS grantee ON grantee.oid = privilege.grantee
WHERE namespace.nspname = ANY($1::text[])
  AND privilege.grantee <> namespace.nspowner
  AND NOT (
    (
      namespace.nspname = $2
      AND grantee.rolname IN ('canonkeeper_product_runtime', 'canonkeeper_governance_owner')
      AND privilege.privilege_type = 'USAGE'
    ) OR (
      namespace.nspname = $3
      AND grantee.rolname = 'canonkeeper_worker_discovery'
      AND privilege.privilege_type = 'USAGE'
    )
  )
ORDER BY namespace.nspname, grantee.rolname, privilege.privilege_type`
	schemas := append([]string{productSchema, workerSchema}, extraSchemas...)
	rows, err := db.Query(ctx, query, schemas, productSchema, workerSchema)
	if err != nil {
		return nil, fmt.Errorf("inspect schema ACLs: %w", err)
	}
	defer rows.Close()
	var violations []string
	for rows.Next() {
		var schema, grantee, privilege string
		if err := rows.Scan(&schema, &grantee, &privilege); err != nil {
			return nil, fmt.Errorf("scan schema ACL violation: %w", err)
		}
		violations = append(violations, fmt.Sprintf("schema %s: %s %s", schema, grantee, privilege))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect schema ACL rows: %w", err)
	}
	return violations, nil
}

func assertSchemaPrivilege(t *testing.T, db migrationQueryer, role, schema, privilege string, want bool) {
	t.Helper()
	var got bool
	if err := db.QueryRow(context.Background(), "SELECT has_schema_privilege($1, $2, $3)", role, schema, privilege).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("schema privilege %s/%s/%s = %t, want %t", role, schema, privilege, got, want)
	}
}

func assertSchemaPublicPrivilege(t *testing.T, db migrationQueryer, schema, privilege string, want bool) {
	t.Helper()
	var got bool
	const query = `
SELECT COALESCE(bool_or(item.grantee = 0 AND item.privilege_type = $2), false)
FROM pg_catalog.pg_namespace AS namespace
CROSS JOIN LATERAL pg_catalog.aclexplode(
  COALESCE(namespace.nspacl, pg_catalog.acldefault('n', namespace.nspowner))
) AS item
WHERE namespace.nspname = $1`
	if err := db.QueryRow(context.Background(), query, schema, privilege).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("schema PUBLIC privilege %s/%s = %t, want %t", schema, privilege, got, want)
	}
}

func assertTablePrivilege(t *testing.T, db migrationQueryer, role, table, privilege string, want bool) {
	t.Helper()
	var got bool
	if err := db.QueryRow(context.Background(), "SELECT has_table_privilege($1, $2, $3)", role, table, privilege).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("table privilege %s/%s/%s = %t, want %t", role, table, privilege, got, want)
	}
}

func assertColumnPrivilege(t *testing.T, db migrationQueryer, role, table, column, privilege string, want bool) {
	t.Helper()
	var got bool
	if err := db.QueryRow(context.Background(), "SELECT has_column_privilege($1, $2, $3, $4)", role, table, column, privilege).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("column privilege %s/%s.%s/%s = %t, want %t", role, table, column, privilege, got, want)
	}
}

func assertTablePublicPrivilege(t *testing.T, db migrationQueryer, table, privilege string, want bool) {
	t.Helper()
	var got bool
	const query = `
SELECT COALESCE(bool_or(item.grantee = 0 AND item.privilege_type = $2), false)
FROM pg_catalog.pg_class AS relation
CROSS JOIN LATERAL pg_catalog.aclexplode(
  COALESCE(relation.relacl, pg_catalog.acldefault('r', relation.relowner))
) AS item
WHERE relation.oid = $1::regclass`
	if err := db.QueryRow(context.Background(), query, table, privilege).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("table PUBLIC privilege %s/%s = %t, want %t", table, privilege, got, want)
	}
}

func assertFunctionPrivilege(t *testing.T, db migrationQueryer, role, function string, want bool) {
	t.Helper()
	var got bool
	if err := db.QueryRow(context.Background(), "SELECT has_function_privilege($1, $2, 'EXECUTE')", role, function).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("function privilege %s/%s = %t, want %t", role, function, got, want)
	}
}

func assertFunctionPublicPrivilege(t *testing.T, db migrationQueryer, function string, want bool) {
	t.Helper()
	var got bool
	const query = `
SELECT COALESCE(bool_or(item.grantee = 0 AND item.privilege_type = 'EXECUTE'), false)
FROM pg_catalog.pg_proc AS procedure
CROSS JOIN LATERAL pg_catalog.aclexplode(
  COALESCE(procedure.proacl, pg_catalog.acldefault('f', procedure.proowner))
) AS item
WHERE procedure.oid = $1::regprocedure`
	if err := db.QueryRow(context.Background(), query, function).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("function PUBLIC privilege %s = %t, want %t", function, got, want)
	}
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
